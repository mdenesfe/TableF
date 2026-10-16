// Package d1 adds Cloudflare D1 through its REST query API.
package d1

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/mdenesfe/TableF/internal/driver"
)

type d1Driver struct {
	driver.NoRoutines
}

func init() { driver.Register(d1Driver{}) }

func (d1Driver) Name() string { return "d1" }

// Features: every HTTP call is its own request with no session, so BEGIN and
// COMMIT can't span statements; edits run one statement at a time.
func (d1Driver) Features() driver.Features {
	return driver.Features{Family: "sqlite", ForeignKeys: true, Groups: true, Edit: true}
}

func dsnOf(cfg driver.Config) dsn {
	return dsn{
		AccountID:  strings.TrimSpace(cfg.Opt("accountId", "")),
		DatabaseID: strings.TrimSpace(cfg.Opt("databaseId", cfg.Database)),
		APIToken:   strings.TrimSpace(cfg.Opt("apiToken", cfg.Password)),
		APIBase:    cfg.Opt("apiBase", ""),
		Name:       cfg.Opt("name", ""),
	}
}

func (d1Driver) Open(cfg driver.Config) (*sql.DB, error) {
	c, err := newClient(dsnOf(cfg))
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector{c}), nil
}

// clientOf reaches the HTTP client behind db (for calls that need config).
func clientOf(ctx context.Context, db *sql.DB) (*client, error) {
	cn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer cn.Close()
	var cl *client
	err = cn.Raw(func(dc any) error {
		c, ok := dc.(*conn)
		if !ok {
			return errors.New("d1: not a D1 connection")
		}
		cl = c.c
		return nil
	})
	return cl, err
}

func (d1Driver) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	cl, err := clientOf(ctx, db)
	if err != nil {
		return nil, err
	}
	return []string{cl.name}, nil
}

func (d1Driver) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	if cl, err := clientOf(ctx, db); err == nil {
		return cl.name
	}
	return ""
}

func (d1Driver) Schemas(context.Context, *sql.DB) ([]string, error) {
	return []string{"main"}, nil
}

// hidden are SQLite internals and Cloudflare's _cf_* bookkeeping tables.
var hidden = []string{"sqlite_", "_cf_"}

func (d1Driver) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	return driver.SQLiteTables(ctx, db, hidden)
}

func (d1Driver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	return driver.SQLiteColumns(ctx, db, schema, table)
}

// ForeignKeys reads all tables' keys in one request.
func (d1Driver) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, []driver.ForeignKey, error) {
	return driver.SQLiteForeignKeys(ctx, db, schema, table, hidden)
}

func (d1Driver) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }
func (d1Driver) Placeholder(int) string        { return "?" }
func (d1Driver) DefaultSchema(driver.Config) string {
	return "main"
}

func (d1Driver) Version(ctx context.Context, db *sql.DB) string {
	if v := driver.QueryString(ctx, db, "SELECT sqlite_version()"); v != "" {
		return "Cloudflare D1 (SQLite " + v + ")"
	}
	return "Cloudflare D1"
}
