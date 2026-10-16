// Package duckdb adds DuckDB (embedded, file or in-memory) through duckdb-go.
package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/mdenesfe/TableF/internal/driver"
)

type duckDriver struct {
	driver.NoRoutines
}

func init() { driver.Register(duckDriver{}) }

func (duckDriver) Name() string { return "duckdb" }

func (duckDriver) Features() driver.Features {
	return driver.Features{Family: "duckdb", Schemas: true, CreateSchema: true, ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// dsn is the database file, "" for an in-memory database. Option readOnly=true
// opens the file with access_mode=READ_ONLY so another process can share it.
//
// duckdb-go takes everything before the first '?' as the path, verbatim (no
// percent-decoding), and parses the whole DSN with url.Parse for its options.
// So a path can't contain '?', and a '%' must not start an invalid escape;
// those paths are rejected with a clear message instead of opening the wrong
// file. '#' and spaces work as they are.
func dsn(cfg driver.Config) (string, error) {
	p := strings.TrimSpace(cfg.FilePath)
	if p == ":memory:" {
		p = ""
	}
	if p == "" {
		return "", nil
	}
	if strings.Contains(p, "?") {
		return "", errors.New("duckdb: file paths containing '?' are not supported; rename or move the file")
	}
	if _, err := url.Parse(p); err != nil {
		return "", fmt.Errorf("duckdb: unsupported file path (%v); rename or move the file", err)
	}
	if strings.EqualFold(cfg.Opt("readOnly", ""), "true") {
		p += "?access_mode=READ_ONLY"
	}
	return p, nil
}

func (duckDriver) Open(cfg driver.Config) (*sql.DB, error) {
	d, err := dsn(cfg)
	if err != nil {
		return nil, err
	}
	return sql.Open("duckdb", d)
}

func (duckDriver) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT database_name FROM duckdb_databases() WHERE NOT internal ORDER BY database_name`)
	if err != nil {
		return nil, err
	}
	out, err := driver.ScanStrings(rows)
	if out == nil {
		out = []string{}
	}
	return out, err
}

const hiddenSchemas = `('information_schema', 'pg_catalog')`

func (duckDriver) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT schema_name FROM information_schema.schemata
		WHERE catalog_name = current_database() AND schema_name NOT IN `+hiddenSchemas+`
		ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	out, err := driver.ScanStrings(rows)
	if out == nil {
		out = []string{}
	}
	return out, err
}

func (duckDriver) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx, `SELECT table_schema, table_name, table_type FROM information_schema.tables
		WHERE table_catalog = current_database() AND table_schema NOT IN `+hiddenSchemas+`
		ORDER BY table_schema, table_name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanTables(rows)
}

func (duckDriver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	if schema == "" {
		schema = "main"
	}
	pks := map[string]bool{}
	prow, err := db.QueryContext(ctx, `SELECT unnest(constraint_column_names) FROM duckdb_constraints()
		WHERE database_name = current_database() AND schema_name = ? AND table_name = ? AND constraint_type = 'PRIMARY KEY'`,
		schema, table)
	if err != nil {
		return nil, err
	}
	names, err := driver.ScanStrings(prow)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		pks[n] = true
	}

	rows, err := db.QueryContext(ctx, `SELECT column_name, data_type, is_nullable, column_default FROM information_schema.columns
		WHERE table_catalog = current_database() AND table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var (
			c        driver.Column
			nullable string
			def      sql.NullString
		)
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &def); err != nil {
			return nil, err
		}
		c.Nullable = strings.EqualFold(nullable, "YES")
		c.PrimaryKey = pks[c.Name]
		if def.Valid {
			c.Default = &def.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ForeignKeys follows single-column FOREIGN KEY constraints from duckdb_constraints().
// DuckDB only allows references inside one schema, so RefSchema is the child's schema.
func (duckDriver) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, []driver.ForeignKey, error) {
	if schema == "" {
		schema = "main"
	}
	rows, err := db.QueryContext(ctx, `SELECT
			COALESCE(constraint_name, table_name || '_' || CAST(constraint_index AS VARCHAR) || '_fkey'),
			schema_name, table_name, constraint_column_names[1],
			schema_name, referenced_table, referenced_column_names[1]
		FROM duckdb_constraints()
		WHERE database_name = current_database() AND constraint_type = 'FOREIGN KEY'
			AND len(constraint_column_names) = 1 AND schema_name = ?
			AND (table_name = ? OR referenced_table = ?)
		ORDER BY table_name, constraint_index`, schema, table, table)
	if err != nil {
		return nil, nil, err
	}
	all, err := driver.ScanForeignKeys(rows)
	if err != nil {
		return nil, nil, err
	}
	out, in := []driver.ForeignKey{}, []driver.ForeignKey{}
	for _, f := range all {
		if f.Table == table {
			out = append(out, f)
		}
		if f.RefTable == table {
			in = append(in, f)
		}
	}
	return out, in, nil
}

func (duckDriver) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }
func (duckDriver) Placeholder(int) string        { return "?" }
func (duckDriver) DefaultSchema(driver.Config) string {
	return "main"
}

func (duckDriver) Version(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT 'DuckDB ' || version()")
}

func (duckDriver) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT current_database()")
}
