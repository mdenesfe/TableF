// Package clickhouse registers the ClickHouse engine (native protocol).
//
// ClickHouse has no schemas inside a database, so the database plays the
// schema's part: the core's "schema"."table" becomes `database`.`table`.
// Edits work through a thin connection wrapper (see conn below).
package clickhouse

import (
	"context"
	"crypto/tls"
	"database/sql"
	sqldriver "database/sql/driver"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/mdenesfe/TableF/internal/driver"
)

type clickhouse struct {
	driver.NoForeignKeys
	driver.NoRoutines
}

func init() { driver.Register(clickhouse{}) }

func (clickhouse) Name() string { return "clickhouse" }

// No transactions, foreign keys or routines. Groups and edits work: edits run
// one statement at a time as lightweight DELETEs and ALTER TABLE ... UPDATE
// mutations (MergeTree tables only).
func (clickhouse) Features() driver.Features {
	return driver.Features{Family: "clickhouse", Groups: true, Edit: true}
}

// options turns a Config into native-protocol options. TLS is used when the
// "secure" option is "true" or the SSL mode asks for it; "require" skips
// certificate checks, like it does for PostgreSQL.
func options(cfg driver.Config) *ch.Options {
	var tlsCfg *tls.Config
	switch {
	case cfg.SSLMode == "require":
		tlsCfg = &tls.Config{InsecureSkipVerify: true}
	case cfg.SSLMode == "verify-ca", cfg.SSLMode == "verify-full", cfg.Opt("secure", "") == "true":
		tlsCfg = &tls.Config{ServerName: cfg.Host}
	}
	port := cfg.Port
	if port == 0 {
		port = 9000
		if tlsCfg != nil {
			port = 9440
		}
	}
	db := cfg.Database
	if db == "" {
		db = "default"
	}
	user := cfg.User
	if user == "" {
		user = "default"
	}
	return &ch.Options{
		Addr:            []string{cfg.Host + ":" + strconv.Itoa(port)},
		Auth:            ch.Auth{Database: db, Username: user, Password: cfg.Password},
		TLS:             tlsCfg,
		DialTimeout:     15 * time.Second,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		ClientInfo: ch.ClientInfo{Products: []struct {
			Name    string
			Version string
		}{{Name: "TableF", Version: "1"}}},
	}
}

func (clickhouse) Open(cfg driver.Config) (*sql.DB, error) {
	return sql.OpenDB(connector{ch.Connector(options(cfg))}), nil
}

// ConfigurePool mirrors the pool sizes in options, which only apply to
// clickhouse-go's own pool, onto the database/sql pool that is used instead.
func (clickhouse) ConfigurePool(db *sql.DB) {
	opt := options(driver.Config{})
	db.SetMaxIdleConns(opt.MaxIdleConns)
	db.SetMaxOpenConns(opt.MaxOpenConns)
	db.SetConnMaxLifetime(opt.ConnMaxLifetime)
}

const systemDatabases = `('system', 'INFORMATION_SCHEMA', 'information_schema')`

func (clickhouse) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM system.databases WHERE name NOT IN `+systemDatabases+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanStrings(rows)
}

// Schemas is unused (Features.Schemas is false); it lists databases anyway.
func (c clickhouse) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	return c.Databases(ctx, db)
}

// Tables lists the current database, like MySQL. Engines View, LiveView and
// MaterializedView all read as views.
func (clickhouse) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT database, name, engine FROM system.tables
		WHERE database = currentDatabase() AND NOT is_temporary AND name NOT LIKE '.inner%'
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanTables(rows)
}

func (clickhouse) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name, type, default_kind, default_expression, is_in_primary_key
		FROM system.columns
		WHERE database = ? AND table = ?
		ORDER BY position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var c driver.Column
		var kind, def string
		var pk uint8
		if err := rows.Scan(&c.Name, &c.Type, &kind, &def, &pk); err != nil {
			return nil, err
		}
		c.Nullable = isNullable(c.Type)
		c.PrimaryKey = pk != 0
		if kind != "" {
			c.Default = &def
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func isNullable(typ string) bool {
	return strings.HasPrefix(typ, "Nullable(") || strings.HasPrefix(typ, "LowCardinality(Nullable(")
}

// QuoteIdent uses backticks; ClickHouse also treats backslash as an escape.
func (clickhouse) QuoteIdent(name string) string {
	return driver.QuoteWith(strings.ReplaceAll(name, `\`, `\\`), "`")
}

func (clickhouse) Placeholder(int) string { return "?" }

func (clickhouse) DefaultSchema(cfg driver.Config) string {
	if cfg.Database != "" {
		return cfg.Database
	}
	return "default"
}

func (clickhouse) Version(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT concat('ClickHouse ', version())")
}

func (clickhouse) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	return driver.QueryString(ctx, db, "SELECT currentDatabase()")
}

// EmptyInsert: ClickHouse has no DEFAULT VALUES, so fail with a clear message.
func (clickhouse) EmptyInsert(string) string {
	return "SELECT throwIf(1, 'ClickHouse can''t insert an empty row; set at least one column value')"
}

// ---------- connection wrapper ----------
//
// The core expects UPDATE ... SET and a RowsAffected of 1. ClickHouse's
// driver always reports 0 rows affected, and only very new servers accept
// UPDATE ... SET. The wrapper rewrites UPDATE into the equivalent
// ALTER TABLE ... UPDATE mutation (waiting for it to finish) and reports
// "rows affected unknown" so the core's one-row check is skipped.

type connector struct{ sqldriver.Connector }

func (c connector) Connect(ctx context.Context) (sqldriver.Conn, error) {
	cn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{cn}, nil
}

type conn struct{ sqldriver.Conn }

func (c *conn) ExecContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	e, ok := c.Conn.(sqldriver.ExecerContext)
	if !ok {
		return nil, sqldriver.ErrSkip
	}
	if q, ok := rewriteUpdate(query); ok {
		query = q
		ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	}
	if _, err := e.ExecContext(ctx, query, args); err != nil {
		return nil, err
	}
	return sqldriver.ResultNoRows, nil
}

func (c *conn) QueryContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	if q, ok := c.Conn.(sqldriver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	return nil, sqldriver.ErrSkip
}

func (c *conn) PrepareContext(ctx context.Context, query string) (sqldriver.Stmt, error) {
	if p, ok := c.Conn.(sqldriver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c *conn) BeginTx(ctx context.Context, opts sqldriver.TxOptions) (sqldriver.Tx, error) {
	if b, ok := c.Conn.(sqldriver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin() //nolint:staticcheck // fallback for drivers without BeginTx
}

func (c *conn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(sqldriver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *conn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(sqldriver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

// CheckNamedValue keeps ClickHouse's own argument handling (arrays, maps, ...).
func (c *conn) CheckNamedValue(nv *sqldriver.NamedValue) error {
	if n, ok := c.Conn.(sqldriver.NamedValueChecker); ok {
		return n.CheckNamedValue(nv)
	}
	return sqldriver.ErrSkip
}

// rewriteUpdate turns `UPDATE t SET a = 1 WHERE ...` into
// `ALTER TABLE t UPDATE a = 1 WHERE ...`. Other statements are left alone.
func rewriteUpdate(q string) (string, bool) {
	s := strings.TrimSpace(q)
	if len(s) < 7 || !strings.EqualFold(s[:7], "UPDATE ") {
		return q, false
	}
	i := topLevelKeyword(s, "SET", 7)
	if i < 0 {
		return q, false
	}
	table := strings.TrimSpace(s[7:i])
	return "ALTER TABLE " + table + " UPDATE " + strings.TrimSpace(s[i+3:]), true
}

// topLevelKeyword finds the whole-word kw in s at or after from, outside
// quotes and backticks.
func topLevelKeyword(s, kw string, from int) int {
	var quote byte
	for i := from; i < len(s); i++ {
		ch := s[i]
		if quote != 0 {
			if ch == '\\' {
				i++
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
			continue
		}
		if i+len(kw) <= len(s) && strings.EqualFold(s[i:i+len(kw)], kw) &&
			(i == 0 || !driver.IsWordByte(s[i-1])) && (i+len(kw) == len(s) || !driver.IsWordByte(s[i+len(kw)])) {
			return i
		}
	}
	return -1
}
