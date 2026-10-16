package driver

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	_ "modernc.org/sqlite"
)

type sqlite struct {
	NoRoutines
}

func init() { Register(sqlite{}) }

// sqliteHidden are SQLite's own bookkeeping tables.
var sqliteHidden = []string{"sqlite_"}

func (sqlite) Name() string { return "sqlite" }

func (sqlite) Features() Features {
	return Features{Family: "sqlite", ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// sqliteURIPath escapes the characters that SQLite's URI parser treats
// specially ('%' escapes, '?' query, '#' fragment), so any file name works.
var sqliteURIPath = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")

// sqliteDSN builds a file: URI for path with foreign keys on and a busy timeout.
func sqliteDSN(path string) string {
	return "file:" + sqliteURIPath.Replace(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}

func (sqlite) Open(cfg Config) (*sql.DB, error) {
	if cfg.FilePath == "" {
		return nil, errors.New("sqlite: file path is required")
	}
	return sql.Open("sqlite", sqliteDSN(cfg.FilePath))
}

// ConfigurePool keeps a single connection: SQLite allows one writer, and one
// connection avoids "database is locked".
func (sqlite) ConfigurePool(db *sql.DB) { db.SetMaxOpenConns(1) }

func (sqlite) Databases(context.Context, *sql.DB) ([]string, error) {
	return []string{"main"}, nil
}

func (sqlite) Schemas(context.Context, *sql.DB) ([]string, error) {
	return []string{"main"}, nil
}

func (sqlite) Tables(ctx context.Context, db *sql.DB) ([]Table, error) {
	return SQLiteTables(ctx, db, sqliteHidden)
}

func (sqlite) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	return SQLiteColumns(ctx, db, schema, table)
}

func (sqlite) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, []ForeignKey, error) {
	return SQLiteForeignKeys(ctx, db, schema, table, sqliteHidden)
}

func (sqlite) QuoteIdent(name string) string { return QuoteWith(name, `"`) }

func (sqlite) Placeholder(int) string { return "?" }

func (sqlite) DefaultSchema(Config) string { return "main" }

func (sqlite) CurrentDatabase(context.Context, *sql.DB) string { return "main" }

func (sqlite) Version(ctx context.Context, db *sql.DB) string {
	return QueryString(ctx, db, "SELECT 'SQLite ' || sqlite_version()")
}
