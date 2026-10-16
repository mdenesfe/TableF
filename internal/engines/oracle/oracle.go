// Package oracle registers the Oracle Database engine (pure-Go go-ora client).
// It needs Oracle 12c or newer: paging uses OFFSET/FETCH and the catalog
// queries filter on ALL_USERS.ORACLE_MAINTAINED.
package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mdenesfe/TableF/internal/driver"
	goora "github.com/sijms/go-ora/v2"
)

type oracle struct{}

func init() { driver.Register(oracle{}) }

func (oracle) Name() string { return "oracle" }

// Schemas are users; Oracle creates them with CREATE USER, so CreateSchema is off.
func (oracle) Features() driver.Features {
	return driver.Features{Family: "oracle", Schemas: true, Routines: true, ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// dsn builds an oracle:// URL; cfg.Database is the service name. Set the
// "sid" option instead to connect by SID.
func dsn(cfg driver.Config) string {
	port := cfg.Port
	if port == 0 {
		port = 1521
	}
	opts := map[string]string{}
	switch cfg.SSLMode {
	case "require":
		opts["SSL"] = "true"
		opts["SSL VERIFY"] = "false"
	case "verify-ca", "verify-full":
		opts["SSL"] = "true"
		opts["SSL VERIFY"] = "true"
	}
	if sid := cfg.Opt("sid", ""); sid != "" {
		opts["SID"] = sid
	}
	if len(opts) == 0 {
		opts = nil
	}
	return goora.BuildUrl(cfg.Host, port, cfg.Database, cfg.User, cfg.Password, opts)
}

func (oracle) Open(cfg driver.Config) (*sql.DB, error) { return sql.Open("oracle", dsn(cfg)) }

// Databases has nothing to switch between: a connection is bound to one
// service, so it lists just that one.
func (oracle) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	if s := currentService(ctx, db); s != "" {
		return []string{s}, nil
	}
	return []string{}, nil
}

func currentService(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, `SELECT SYS_CONTEXT('USERENV', 'SERVICE_NAME') FROM DUAL`)
}

// userSchemas are owners that aren't shipped with Oracle (SYS, XDB, ...).
const userSchemas = `(SELECT username FROM all_users WHERE oracle_maintained = 'N' OR username = USER)`

func (oracle) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT u.username FROM all_users u
		WHERE u.username IN `+userSchemas+`
		  AND (u.username = USER OR EXISTS (SELECT 1 FROM all_objects o WHERE o.owner = u.username))
		ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	return driver.ScanStrings(rows)
}

func (oracle) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT owner, table_name, 'table' FROM all_tables
		WHERE owner IN `+userSchemas+`
		  AND nested = 'NO' AND secondary = 'N' AND dropped = 'NO'
		  AND (iot_type IS NULL OR iot_type = 'IOT')
		UNION ALL
		SELECT owner, view_name, 'view' FROM all_views
		WHERE owner IN `+userSchemas+`
		ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	return driver.ScanTables(rows)
}

func (oracle) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.column_name, c.data_type, c.nullable, c.data_default,
		       CASE WHEN EXISTS (
		         SELECT 1 FROM all_constraints k
		         JOIN all_cons_columns kc ON kc.owner = k.owner AND kc.constraint_name = k.constraint_name
		         WHERE k.constraint_type = 'P' AND k.owner = c.owner AND k.table_name = c.table_name
		           AND kc.column_name = c.column_name
		       ) THEN 'Y' ELSE 'N' END
		FROM all_tab_columns c
		WHERE c.owner = :1 AND c.table_name = :2
		ORDER BY c.column_id`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var c driver.Column
		var nullable, pk string
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &def, &pk); err != nil {
			return nil, err
		}
		c.Nullable, c.PrimaryKey = nullable == "Y", pk == "Y"
		if def.Valid {
			d := strings.TrimSpace(def.String) // DATA_DEFAULT keeps the DDL's trailing whitespace
			c.Default = &d
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Only single-column keys are listed, like the other engines: grouping by
// constraint and keeping groups of one row drops composite keys.
const foreignKeys = `
	SELECT c.constraint_name, c.owner, c.table_name, MAX(cc.column_name),
	       MAX(r.owner), MAX(r.table_name), MAX(rc.column_name)
	FROM all_constraints c
	JOIN all_cons_columns cc ON cc.owner = c.owner AND cc.constraint_name = c.constraint_name
	JOIN all_constraints r ON r.owner = c.r_owner AND r.constraint_name = c.r_constraint_name
	JOIN all_cons_columns rc ON rc.owner = r.owner AND rc.constraint_name = r.constraint_name AND rc.position = cc.position
	WHERE c.constraint_type = 'R' AND %s
	GROUP BY c.owner, c.constraint_name, c.table_name
	HAVING COUNT(*) = 1
	ORDER BY 2, 3, 4`

func (oracle) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, []driver.ForeignKey, error) {
	query := func(cond string) ([]driver.ForeignKey, error) {
		rows, err := db.QueryContext(ctx, fmt.Sprintf(foreignKeys, cond), schema, table)
		if err != nil {
			return nil, err
		}
		return driver.ScanForeignKeys(rows)
	}
	out, err := query(`c.owner = :1 AND c.table_name = :2`)
	if err != nil {
		return nil, nil, err
	}
	in, err := query(`r.owner = :1 AND r.table_name = :2`)
	return out, in, err
}

// Routines lists standalone functions and procedures (not package members).
func (oracle) Routines(ctx context.Context, db *sql.DB) ([]driver.Routine, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT o.owner || '.' || o.object_name, o.owner, o.object_name, LOWER(o.object_type),
		       COALESCE((SELECT LISTAGG(a.argument_name || ' ' || a.in_out || ' ' || a.data_type, ', ')
		                   WITHIN GROUP (ORDER BY a.position)
		                 FROM all_arguments a
		                 WHERE a.owner = o.owner AND a.object_name = o.object_name
		                   AND a.package_name IS NULL AND a.data_level = 0 AND a.position > 0), ''),
		       COALESCE((SELECT MAX(a.data_type) FROM all_arguments a
		                 WHERE a.owner = o.owner AND a.object_name = o.object_name
		                   AND a.package_name IS NULL AND a.data_level = 0 AND a.position = 0), '')
		FROM all_objects o
		WHERE o.object_type IN ('FUNCTION', 'PROCEDURE')
		  AND o.owner IN `+userSchemas+`
		ORDER BY 2, 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Routine{}
	for rows.Next() {
		var r driver.Routine
		if err := rows.Scan(&r.ID, &r.Schema, &r.Name, &r.Kind, &r.Arguments, &r.ReturnType); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (oracle) RoutineDefinition(ctx context.Context, db *sql.DB, r driver.Routine) (string, error) {
	var def string
	err := db.QueryRowContext(ctx, `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM DUAL`,
		strings.ToUpper(r.Kind), r.Name, r.Schema).Scan(&def)
	return strings.TrimSpace(def), err
}

func (oracle) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }

func (oracle) Placeholder(n int) string { return fmt.Sprintf(":%d", n) }

// DefaultSchema is the login user; unquoted Oracle names are upper case.
func (oracle) DefaultSchema(cfg driver.Config) string { return strings.ToUpper(cfg.User) }

func (oracle) Paginate(query string, limit, offset int) (string, int) {
	return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", query, offset, limit), 0
}

func (oracle) Version(ctx context.Context, db *sql.DB) string {
	if v := driver.QueryString(ctx, db, `SELECT banner FROM v$version WHERE ROWNUM = 1`); v != "" {
		return v
	}
	return driver.QueryString(ctx, db, `
		SELECT product || ' ' || version FROM product_component_version
		WHERE product LIKE 'Oracle%' AND ROWNUM = 1`)
}

func (oracle) CurrentDatabase(ctx context.Context, db *sql.DB) string { return currentService(ctx, db) }

// EmptyInsert: Oracle has no INSERT ... DEFAULT VALUES. The block looks up the
// table's first column and inserts DEFAULT into it, which fills every other
// column with its default too. table is the quoted "SCHEMA"."TABLE" name.
func (oracle) EmptyInsert(table string) string {
	parts := splitQualified(table)
	if len(parts) != 2 {
		return `BEGIN RAISE_APPLICATION_ERROR(-20000, 'Oracle can''t insert an empty row; set at least one column value'); END;`
	}
	lit := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	return fmt.Sprintf(`DECLARE c VARCHAR2(128);
BEGIN
  SELECT column_name INTO c FROM all_tab_columns WHERE owner = %s AND table_name = %s AND column_id = 1;
  EXECUTE IMMEDIATE %s || REPLACE(c, '"', '""') || '") VALUES (DEFAULT)';
END;`, lit(parts[0]), lit(parts[1]), lit("INSERT INTO "+table+` ("`))
}

// splitQualified parses `"A"."B"` back into its unquoted parts.
func splitQualified(s string) []string {
	var parts []string
	for len(s) > 0 {
		if s[0] != '"' {
			return nil
		}
		var b strings.Builder
		i := 1
		for ; i < len(s); i++ {
			if s[i] == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					b.WriteByte('"')
					i++
					continue
				}
				break
			}
			b.WriteByte(s[i])
		}
		if i >= len(s) {
			return nil // unterminated
		}
		parts = append(parts, b.String())
		s = s[i+1:]
		if len(s) > 0 {
			if s[0] != '.' {
				return nil
			}
			s = s[1:]
		}
	}
	return parts
}
