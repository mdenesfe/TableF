// Package libsql adds Turso / libSQL (remote SQLite over HTTP or WebSocket).
package libsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

type libsqlDriver struct {
	driver.NoRoutines
}

func init() { driver.Register(libsqlDriver{}) }

func (libsqlDriver) Name() string { return "libsql" }

func (libsqlDriver) Features() driver.Features {
	// Transactions: the HTTP client keeps a Hrana stream (baton) per connection,
	// so BEGIN/COMMIT on one *sql.Tx run on the same server-side stream.
	return driver.Features{Family: "sqlite", ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// dsn builds the URL handed to the libsql database/sql driver. The token goes
// in the authToken query parameter, which that driver strips before dialing.
func dsn(cfg driver.Config) (string, error) {
	raw := strings.TrimSpace(cfg.Opt("url", cfg.Host))
	if raw == "" {
		return "", errors.New("libsql: url is required (libsql://, https:// or http://)")
	}
	if !strings.Contains(raw, "://") {
		raw = "libsql://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("libsql: invalid url: %w", err)
	}
	switch u.Scheme {
	case "libsql", "https", "http", "wss", "ws":
	default:
		return "", fmt.Errorf("libsql: unsupported url scheme %q (use libsql://, https:// or http://)", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("libsql: url has no host")
	}
	q := u.Query()
	if tok := cfg.Opt("authToken", ""); tok != "" {
		q.Del("auth_token")
		q.Del("jwt")
		q.Set("authToken", tok)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (libsqlDriver) Open(cfg driver.Config) (*sql.DB, error) {
	d, err := dsn(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("libsql", d)
	if err != nil {
		// The libsql driver may echo the URL; don't leak the token.
		return nil, errors.New("libsql: " + redact(err.Error(), cfg.Opt("authToken", "")))
	}
	return db, nil
}

// ConfigurePool: Hrana streams expire on the server after a short idle
// period; retire idle connections before that so a pooled conn never holds a
// dead baton.
func (libsqlDriver) ConfigurePool(db *sql.DB) {
	db.SetMaxOpenConns(driver.DefaultMaxOpenConns)
	db.SetConnMaxIdleTime(5 * time.Second)
	db.SetMaxIdleConns(2)
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	s = strings.ReplaceAll(s, secret, "***")
	return strings.ReplaceAll(s, url.QueryEscape(secret), "***")
}

func (libsqlDriver) Databases(context.Context, *sql.DB) ([]string, error) {
	return []string{"main"}, nil
}

func (libsqlDriver) Schemas(context.Context, *sql.DB) ([]string, error) {
	return []string{"main"}, nil
}

// hidden are SQLite, Litestream and libSQL bookkeeping tables.
var hidden = []string{"sqlite_", "_litestream", "libsql_"}

func (libsqlDriver) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	return driver.SQLiteTables(ctx, db, hidden)
}

func (libsqlDriver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	return driver.SQLiteColumns(ctx, db, schema, table)
}

// ForeignKeys reads every table's foreign keys in one round trip (one HTTP
// request instead of one per table).
func (libsqlDriver) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, []driver.ForeignKey, error) {
	return driver.SQLiteForeignKeys(ctx, db, schema, table, hidden)
}

func (libsqlDriver) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }
func (libsqlDriver) Placeholder(int) string        { return "?" }
func (libsqlDriver) DefaultSchema(driver.Config) string {
	return "main"
}

func (libsqlDriver) Version(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT 'libSQL ' || sqlite_version()")
}

func (libsqlDriver) CurrentDatabase(context.Context, *sql.DB) string { return "main" }
