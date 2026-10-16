// Package mssql registers the Microsoft SQL Server engine.
package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/mdenesfe/TableF/internal/driver"
	_ "github.com/microsoft/go-mssqldb"
)

type mssql struct{}

func init() { driver.Register(mssql{}) }

func (mssql) Name() string { return "mssql" }

func (mssql) Features() driver.Features {
	return driver.Features{Family: "mssql", Schemas: true, CreateSchema: true, Routines: true, ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// dsn builds a sqlserver:// URL. Without an SSL mode only the login packet
// is encrypted (encrypt=false), so the password never crosses the network in
// clear text; "disable" turns encryption off entirely. "require" encrypts
// everything and trusts the server certificate; the verify modes check it.
func dsn(cfg driver.Config) string {
	port := cfg.Port
	if port == 0 {
		port = 1433
	}
	encrypt, trust := "false", "true"
	switch cfg.SSLMode {
	case "disable":
		encrypt = "disable"
	case "require":
		encrypt = "true"
	case "verify-full", "verify-ca":
		encrypt, trust = "true", "false"
	}
	q := url.Values{
		"encrypt":                {cfg.Opt("encrypt", encrypt)},
		"TrustServerCertificate": {cfg.Opt("trustServerCertificate", trust)},
		"app name":               {"TableF"},
	}
	if cfg.Database != "" {
		q.Set("database", cfg.Database)
	}
	u := url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, port),
		RawQuery: q.Encode(),
	}
	return u.String()
}

func (mssql) Open(cfg driver.Config) (*sql.DB, error) { return sql.Open("sqlserver", dsn(cfg)) }

func (mssql) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name FROM sys.databases
		WHERE name NOT IN ('master', 'tempdb', 'model', 'msdb') AND state_desc = 'ONLINE'
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanStrings(rows)
}

// Built-in roles show up in sys.schemas too; keep dbo, guest-owned user schemas.
const systemSchemas = `('sys', 'INFORMATION_SCHEMA', 'guest', 'db_owner', 'db_accessadmin',
	'db_securityadmin', 'db_ddladmin', 'db_backupoperator', 'db_datareader',
	'db_datawriter', 'db_denydatareader', 'db_denydatawriter')`

func (mssql) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sys.schemas WHERE name NOT IN `+systemSchemas+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanStrings(rows)
}

func (mssql) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA NOT IN `+systemSchemas+`
		ORDER BY TABLE_SCHEMA, TABLE_NAME`)
	if err != nil {
		return nil, err
	}
	return driver.ScanTables(rows)
}

func (mssql) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.COLUMN_NAME,
		       c.DATA_TYPE,
		       CAST(CASE WHEN c.IS_NULLABLE = 'YES' THEN 1 ELSE 0 END AS bit),
		       c.COLUMN_DEFAULT,
		       CAST(CASE WHEN EXISTS (
		         SELECT 1
		         FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
		         JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE k
		           ON k.CONSTRAINT_NAME = tc.CONSTRAINT_NAME AND k.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
		         WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY'
		           AND tc.TABLE_SCHEMA = c.TABLE_SCHEMA AND tc.TABLE_NAME = c.TABLE_NAME
		           AND k.COLUMN_NAME = c.COLUMN_NAME
		       ) THEN 1 ELSE 0 END AS bit)
		FROM INFORMATION_SCHEMA.COLUMNS c
		WHERE c.TABLE_SCHEMA = @p1 AND c.TABLE_NAME = @p2
		ORDER BY c.ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var c driver.Column
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &def, &c.PrimaryKey); err != nil {
			return nil, err
		}
		if def.Valid {
			c.Default = &def.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Only single-column keys are listed, like the other engines: grouping by
// constraint and keeping groups of one row drops composite keys.
const foreignKeys = `
	SELECT fk.name, SCHEMA_NAME(pt.schema_id), pt.name, MAX(pc.name),
	       SCHEMA_NAME(rt.schema_id), rt.name, MAX(rc.name)
	FROM sys.foreign_keys fk
	JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
	JOIN sys.tables pt ON pt.object_id = fkc.parent_object_id
	JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
	JOIN sys.tables rt ON rt.object_id = fkc.referenced_object_id
	JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
	WHERE %s
	GROUP BY fk.object_id, fk.name, pt.schema_id, pt.name, rt.schema_id, rt.name
	HAVING COUNT(*) = 1
	ORDER BY 2, 3, 4`

func (mssql) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, []driver.ForeignKey, error) {
	query := func(cond string) ([]driver.ForeignKey, error) {
		rows, err := db.QueryContext(ctx, fmt.Sprintf(foreignKeys, cond), schema, table)
		if err != nil {
			return nil, err
		}
		return driver.ScanForeignKeys(rows)
	}
	out, err := query(`SCHEMA_NAME(pt.schema_id) = @p1 AND pt.name = @p2`)
	if err != nil {
		return nil, nil, err
	}
	in, err := query(`SCHEMA_NAME(rt.schema_id) = @p1 AND rt.name = @p2`)
	return out, in, err
}

// Routines lists procedures and functions. Arguments are joined with
// FOR XML PATH so it works before SQL Server 2017 (no STRING_AGG).
func (mssql) Routines(ctx context.Context, db *sql.DB) ([]driver.Routine, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT CAST(o.object_id AS varchar(20)), SCHEMA_NAME(o.schema_id), o.name,
		       CASE WHEN o.type = 'P' THEN 'procedure' ELSE 'function' END,
		       COALESCE(STUFF((
		         SELECT ', ' + p.name + ' ' + TYPE_NAME(p.user_type_id)
		         FROM sys.parameters p
		         WHERE p.object_id = o.object_id AND p.parameter_id > 0
		         ORDER BY p.parameter_id
		         FOR XML PATH(''), TYPE).value('.', 'nvarchar(max)'), 1, 2, ''), ''),
		       CASE WHEN o.type IN ('IF', 'TF') THEN 'TABLE'
		            ELSE COALESCE((SELECT TYPE_NAME(p.user_type_id) FROM sys.parameters p
		                           WHERE p.object_id = o.object_id AND p.parameter_id = 0), '') END
		FROM sys.objects o
		WHERE o.type IN ('P', 'FN', 'IF', 'TF') AND o.is_ms_shipped = 0
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

func (mssql) RoutineDefinition(ctx context.Context, db *sql.DB, r driver.Routine) (string, error) {
	var def sql.NullString
	err := db.QueryRowContext(ctx, `SELECT OBJECT_DEFINITION(CAST(@p1 AS int))`, r.ID).Scan(&def)
	if err == nil && !def.Valid {
		return "", fmt.Errorf("definition of %s.%s is not available (encrypted or no permission)", r.Schema, r.Name)
	}
	return def.String, err
}

func (mssql) QuoteIdent(name string) string { return "[" + strings.ReplaceAll(name, "]", "]]") + "]" }

func (mssql) Placeholder(n int) string { return fmt.Sprintf("@p%d", n) }

func (mssql) DefaultSchema(driver.Config) string { return "dbo" }

func (mssql) EmptyInsert(table string) string { return "INSERT INTO " + table + " DEFAULT VALUES" }

func (mssql) Version(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT @@VERSION")
}

func (mssql) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT DB_NAME()")
}

// Paginate uses OFFSET/FETCH, which SQL Server only accepts after an ORDER BY.
func (mssql) Paginate(query string, limit, offset int) (string, int) {
	if !hasOrderBy(query) {
		query += " ORDER BY (SELECT NULL)"
	}
	return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", query, offset, limit), 0
}

// hasOrderBy reports whether query has an ORDER BY outside parentheses,
// string literals and quoted identifiers.
func hasOrderBy(query string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '[':
			quote = ']'
		case '(':
			depth++
		case ')':
			depth--
		case 'o', 'O':
			if depth == 0 && (i == 0 || !driver.IsWordByte(query[i-1])) && isOrderBy(query[i:]) {
				return true
			}
		}
	}
	return false
}

func isOrderBy(s string) bool {
	if len(s) < 5 || !strings.EqualFold(s[:5], "order") {
		return false
	}
	rest := strings.TrimLeft(s[5:], " \t\r\n")
	if len(rest) == len(s)-5 { // no whitespace after ORDER
		return false
	}
	return len(rest) >= 2 && strings.EqualFold(rest[:2], "by") && (len(rest) == 2 || !driver.IsWordByte(rest[2]))
}
