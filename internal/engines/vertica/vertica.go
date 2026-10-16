// Package vertica registers the Vertica engine.
package vertica

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"

	"github.com/mdenesfe/TableF/internal/driver"
	_ "github.com/vertica/vertica-sql-go"
)

type vertica struct {
	driver.NoRoutines
}

func init() { driver.Register(vertica{}) }

func (vertica) Name() string { return "vertica" }

// Vertica's catalog has no stored procedures worth listing (they're external).
func (vertica) Features() driver.Features {
	return driver.Features{Family: "vertica", Schemas: true, CreateSchema: true, ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// tlsMode maps the shared SSL modes onto vertica-sql-go's tlsmode values.
// An empty mode stays "none" (plain TCP, the driver's own default) because
// many Vertica installs have no TLS configured and "prefer" would still fail
// there with some server versions; pick "prefer" or stricter to encrypt.
func tlsMode(cfg driver.Config) string {
	switch cfg.SSLMode {
	case "prefer":
		return "prefer"
	case "require":
		return "server"
	case "verify-ca", "verify-full":
		return "server-strict"
	}
	return "none"
}

func dsn(cfg driver.Config) string {
	port := cfg.Port
	if port == 0 {
		port = 5433
	}
	u := url.URL{
		Scheme:   "vertica",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     cfg.Host + ":" + strconv.Itoa(port),
		Path:     "/" + cfg.Database,
		RawQuery: url.Values{"tlsmode": {cfg.Opt("tlsmode", tlsMode(cfg))}, "client_label": {"TableF"}}.Encode(),
	}
	return u.String()
}

func (vertica) Open(cfg driver.Config) (*sql.DB, error) { return sql.Open("vertica", dsn(cfg)) }

func (vertica) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT database_name FROM v_catalog.databases ORDER BY database_name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanStrings(rows)
}

func (vertica) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT schema_name FROM v_catalog.schemata
		WHERE NOT is_system_schema AND schema_name NOT LIKE 'v\_%'
		ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanStrings(rows)
}

func (vertica) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_schema, table_name, 'table' FROM v_catalog.tables
		WHERE NOT is_system_table AND table_schema NOT LIKE 'v\_%'
		UNION ALL
		SELECT table_schema, table_name, 'view' FROM v_catalog.views
		WHERE NOT is_system_view AND table_schema NOT LIKE 'v\_%'
		ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	return driver.ScanTables(rows)
}

// Columns covers tables and views (v_catalog.columns only has tables).
func (vertica) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.column_name, c.data_type, c.is_nullable, c.column_default,
		       EXISTS (SELECT 1 FROM v_catalog.primary_keys k
		               WHERE k.table_schema = c.table_schema AND k.table_name = c.table_name
		                 AND k.column_name = c.column_name),
		       c.ordinal_position
		FROM v_catalog.columns c
		WHERE c.table_schema = ? AND c.table_name = ?
		UNION ALL
		SELECT v.column_name, v.data_type, true, NULL, false, v.ordinal_position
		FROM v_catalog.view_columns v
		WHERE v.table_schema = ? AND v.table_name = ?
		ORDER BY 6`, schema, table, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var c driver.Column
		var def sql.NullString
		var pos int64
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &def, &c.PrimaryKey, &pos); err != nil {
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
	SELECT constraint_name, MAX(table_schema), MAX(table_name), MAX(column_name),
	       MAX(reference_table_schema), MAX(reference_table_name), MAX(reference_column_name)
	FROM v_catalog.foreign_keys
	WHERE %s
	GROUP BY constraint_id, constraint_name
	HAVING COUNT(*) = 1
	ORDER BY 2, 3, 4`

func (vertica) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, []driver.ForeignKey, error) {
	query := func(cond string) ([]driver.ForeignKey, error) {
		rows, err := db.QueryContext(ctx, fmt.Sprintf(foreignKeys, cond), schema, table)
		if err != nil {
			return nil, err
		}
		return driver.ScanForeignKeys(rows)
	}
	out, err := query(`table_schema = ? AND table_name = ?`)
	if err != nil {
		return nil, nil, err
	}
	in, err := query(`reference_table_schema = ? AND reference_table_name = ?`)
	return out, in, err
}

func (vertica) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }

func (vertica) Placeholder(int) string { return "?" }

func (vertica) DefaultSchema(driver.Config) string { return "public" }

func (vertica) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT CURRENT_DATABASE()")
}
