// Package snowflake registers the Snowflake engine.
//
// Connections use the "account" option (e.g. "xy12345.eu-central-1") with
// cfg.User/Password/Database, plus optional "warehouse", "role" and "schema".
// Catalog queries read the current database's INFORMATION_SCHEMA.
package snowflake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mdenesfe/TableF/internal/driver"
	sf "github.com/snowflakedb/gosnowflake"
)

type snowflake struct {
	driver.NoForeignKeys
	driver.NoRoutines
}

func init() { driver.Register(snowflake{}) }

func (snowflake) Name() string { return "snowflake" }

// Snowflake's foreign keys are informational only, so they aren't listed.
func (snowflake) Features() driver.Features {
	return driver.Features{Family: "snowflake", Schemas: true, CreateSchema: true, Groups: true, Edit: true, Transactions: true}
}

func dsn(cfg driver.Config) (string, error) {
	account := cfg.Opt("account", "")
	if account == "" {
		return "", errors.New("snowflake: the account option is required (e.g. xy12345.eu-central-1)")
	}
	return sf.DSN(&sf.Config{
		Account:     account,
		User:        cfg.User,
		Password:    cfg.Password,
		Database:    cfg.Database,
		Schema:      cfg.Opt("schema", ""),
		Warehouse:   cfg.Opt("warehouse", ""),
		Role:        cfg.Opt("role", ""),
		Application: "TableF",
	})
}

func (snowflake) Open(cfg driver.Config) (*sql.DB, error) {
	d, err := dsn(cfg)
	if err != nil {
		return nil, err
	}
	return sql.Open("snowflake", d)
}

// Databases reads SHOW DATABASES, which works without a current database.
func (snowflake) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SHOW DATABASES`)
	if err != nil {
		return nil, err
	}
	return column(rows, "name")
}

func (snowflake) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name <> 'INFORMATION_SCHEMA'
		ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanStrings(rows)
}

func (snowflake) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_schema, table_name, table_type
		FROM information_schema.tables
		WHERE table_schema <> 'INFORMATION_SCHEMA'
		ORDER BY table_schema, table_name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanTables(rows)
}

func (s snowflake) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, data_type, is_nullable = 'YES', column_default
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var c driver.Column
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &def); err != nil {
			return nil, err
		}
		if def.Valid {
			c.Default = &def.String
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// INFORMATION_SCHEMA has no key columns; SHOW PRIMARY KEYS does. Views
	// and missing privileges make it fail, which just means no key.
	if pk, err := db.QueryContext(ctx, `SHOW PRIMARY KEYS IN TABLE `+driver.QualifiedName(s, schema, table)); err == nil {
		if names, err := column(pk, "column_name"); err == nil {
			keys := map[string]bool{}
			for _, n := range names {
				keys[n] = true
			}
			for i := range out {
				out[i].PrimaryKey = keys[out[i].Name]
			}
		}
	}
	return out, nil
}

func (snowflake) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }

func (snowflake) Placeholder(int) string { return "?" }

func (snowflake) DefaultSchema(cfg driver.Config) string { return cfg.Opt("schema", "PUBLIC") }

func (snowflake) Version(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT 'Snowflake ' || CURRENT_VERSION()")
}

func (snowflake) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT CURRENT_DATABASE()")
}

// EmptyInsert: Snowflake has no INSERT ... DEFAULT VALUES and VALUES (DEFAULT)
// needs a column list, so raise a clear error instead.
func (snowflake) EmptyInsert(string) string {
	return `EXECUTE IMMEDIATE $$
DECLARE empty_row EXCEPTION (-20001, 'Snowflake can''t insert an empty row; set at least one column value');
BEGIN
  RAISE empty_row;
END;
$$`
}

// column reads one named column (case-insensitive) from rows of any shape,
// as returned by SHOW commands.
func column(rows *sql.Rows, name string) ([]string, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, c := range cols {
		if strings.EqualFold(c, name) {
			idx = i
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("snowflake: no %q column in result", name)
	}
	out := []string{}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, vals[idx].String)
	}
	return out, rows.Err()
}
