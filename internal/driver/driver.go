// Package driver abstracts the differences between supported database engines.
package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Config describes how to reach a database. It is persisted as a saved connection.
type Config struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Driver   string `json:"driver"` // engine id, see Engines
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	SSLMode  string `json:"sslMode"`
	FilePath string `json:"filePath"` // file-based engines (sqlite, duckdb)
	Color    string `json:"color"`
	// Tags group and filter saved connections, e.g. "local", "staging", "production".
	Tags []string `json:"tags"`
	// Options holds engine-specific settings, e.g. accountId and apiToken for
	// Cloudflare D1, projectId and credentials for BigQuery, region and keys for
	// DynamoDB, warehouse and role for Snowflake, url and authToken for libSQL.
	Options map[string]string `json:"options"`

	// SSH tunnel. The database host/port are resolved from the SSH server.
	SSHEnabled    bool   `json:"sshEnabled"`
	SSHHost       string `json:"sshHost"`
	SSHPort       int    `json:"sshPort"`
	SSHUser       string `json:"sshUser"`
	SSHAuth       string `json:"sshAuth"` // password | key | agent
	SSHPassword   string `json:"sshPassword"`
	SSHKeyFile    string `json:"sshKeyFile"`
	SSHPassphrase string `json:"sshPassphrase"`

	// Secrets names the secret fields that are set on the saved connection
	// when it is sent to the UI with those fields blanked out: "password",
	// "sshPassword", "sshPassphrase", "options.<key>" and
	// "options.uri.password". It is only ever filled in for the UI and is
	// never persisted.
	Secrets []string `json:"secrets,omitempty"`
}

// Opt returns an engine option, or def when it is empty.
func (c Config) Opt(key, def string) string {
	if v := c.Options[key]; v != "" {
		return v
	}
	return def
}

var defaultPorts = map[string]int{
	"postgres": 5432, "redshift": 5439, "cockroach": 26257, "greenplum": 5432,
	"mysql": 3306, "mariadb": 3306, "singlestore": 3306,
	"mssql": 1433, "oracle": 1521, "clickhouse": 9000, "vertica": 5433,
	"cassandra": 9042, "elasticsearch": 9200, "mongodb": 27017, "redis": 6379,
}

// DefaultPort returns the standard port of a network engine, 0 when unknown.
func DefaultPort(engine string) int { return defaultPorts[engine] }

// UsesNetwork reports whether the engine dials Host:Port (and so can go over SSH).
func UsesNetwork(engine string) bool {
	switch engine {
	case "sqlite", "duckdb", "d1", "bigquery", "dynamodb", "libsql", "snowflake":
		return false
	}
	return true
}

// Table is a table or view inside a schema.
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"` // table | view
}

// Routine is a stored function or procedure.
type Routine struct {
	ID         string `json:"id"` // engine-specific handle used to fetch the definition
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // function | procedure
	Arguments  string `json:"arguments"`
	ReturnType string `json:"returnType"`
}

// ForeignKey links Column of a table to RefColumn of RefSchema.RefTable.
// For references pointing at a table (ReferencedBy), Schema/Table name the child.
type ForeignKey struct {
	Name      string `json:"name"`
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	Column    string `json:"column"`
	RefSchema string `json:"refSchema"`
	RefTable  string `json:"refTable"`
	RefColumn string `json:"refColumn"`
}

// Column describes one column of a table.
type Column struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Nullable   bool    `json:"nullable"`
	Default    *string `json:"default"`
	PrimaryKey bool    `json:"primaryKey"`
}

// Driver implements the engine-specific parts. Everything that can be done with
// plain SQL lives in the core package instead.
type Driver interface {
	Name() string
	Open(cfg Config) (*sql.DB, error)
	Databases(ctx context.Context, db *sql.DB) ([]string, error)
	Tables(ctx context.Context, db *sql.DB) ([]Table, error)
	Schemas(ctx context.Context, db *sql.DB) ([]string, error)
	Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error)
	// ForeignKeys returns the table's outgoing references and the references pointing at it.
	ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) (out, in []ForeignKey, err error)
	Routines(ctx context.Context, db *sql.DB) ([]Routine, error)
	// RoutineDefinition returns the CREATE statement of a routine.
	RoutineDefinition(ctx context.Context, db *sql.DB, r Routine) (string, error)
	// QuoteIdent quotes a single identifier (table, column, schema).
	QuoteIdent(name string) string
	// Placeholder returns the bind parameter for the n-th (1-based) argument.
	Placeholder(n int) string
	// DefaultSchema is used when a table reference has no schema.
	DefaultSchema(cfg Config) string
}

// Features tells the core and the UI what an engine can do.
type Features struct {
	// Family is the query language/dialect: postgres, mysql, sqlite, mssql,
	// oracle, clickhouse, snowflake, bigquery, duckdb, vertica, cassandra,
	// partiql, elasticsearch, mongodb or redis.
	Family       string `json:"family"`
	Schemas      bool   `json:"schemas"`
	CreateSchema bool   `json:"createSchema"`
	Routines     bool   `json:"routines"`
	ForeignKeys  bool   `json:"foreignKeys"`
	Groups       bool   `json:"groups"`
	Edit         bool   `json:"edit"`
	Transactions bool   `json:"transactions"`
}

// Optional hooks a Driver may implement. The core falls back to standard SQL.
type (
	// Featurer reports capabilities; without it a driver gets SQL defaults.
	Featurer interface{ Features() Features }
	// Versioner returns a server version string (default: SELECT version()).
	Versioner interface {
		Version(ctx context.Context, db *sql.DB) string
	}
	// CurrentDatabaser names the database the session is in.
	CurrentDatabaser interface {
		CurrentDatabase(ctx context.Context, db *sql.DB) string
	}
	// Paginator adds paging to a SELECT (default: LIMIT n OFFSET m). Engines
	// without OFFSET may return just a limit of limit+offset; the core then
	// drops the first offset rows itself when SkipsOffset is true.
	Paginator interface {
		Paginate(query string, limit, offset int) (q string, skip int)
	}
	// EmptyInserter returns the statement that inserts a row of defaults.
	EmptyInserter interface{ EmptyInsert(table string) string }
	// PoolConfigurer sizes the connection pool of a freshly opened *sql.DB
	// itself (SQLite's single writer, HTTP engines with short-lived
	// sessions). Without it the core applies DefaultPool.
	PoolConfigurer interface{ ConfigurePool(db *sql.DB) }
)

// Connection pool defaults applied by DefaultPool.
const (
	DefaultMaxOpenConns    = 8
	DefaultConnMaxIdleTime = 5 * time.Minute
	DefaultConnMaxLifetime = 30 * time.Minute
)

// DefaultPool bounds a pool so a busy UI can't open an unbounded number of
// server connections and idle ones are eventually returned to the server.
func DefaultPool(db *sql.DB) {
	db.SetMaxOpenConns(DefaultMaxOpenConns)
	db.SetConnMaxIdleTime(DefaultConnMaxIdleTime)
	db.SetConnMaxLifetime(DefaultConnMaxLifetime)
}

// ConfigurePool applies the driver's own pool settings, or DefaultPool.
func ConfigurePool(d Driver, db *sql.DB) {
	if p, ok := d.(PoolConfigurer); ok {
		p.ConfigurePool(db)
		return
	}
	DefaultPool(db)
}

// NoRoutines can be embedded by engines without stored routines.
type NoRoutines struct{}

// Routines returns an empty list.
func (NoRoutines) Routines(context.Context, *sql.DB) ([]Routine, error) { return []Routine{}, nil }

// RoutineDefinition always fails: there is nothing to define.
func (NoRoutines) RoutineDefinition(context.Context, *sql.DB, Routine) (string, error) {
	return "", errors.New("this database has no stored routines")
}

// NoForeignKeys can be embedded by engines without (enforced) foreign keys.
type NoForeignKeys struct{}

// ForeignKeys returns no references in either direction.
func (NoForeignKeys) ForeignKeys(context.Context, *sql.DB, string, string) ([]ForeignKey, []ForeignKey, error) {
	return []ForeignKey{}, []ForeignKey{}, nil
}

// FeaturesOf returns the driver's features, with SQL defaults.
func FeaturesOf(d Driver) Features {
	if f, ok := d.(Featurer); ok {
		return f.Features()
	}
	return Features{Family: d.Name(), Schemas: true, ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// Paginate applies the driver's paging to q.
func Paginate(d Driver, q string, limit, offset int) (string, int) {
	if p, ok := d.(Paginator); ok {
		return p.Paginate(q, limit, offset)
	}
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", q, limit, offset), 0
}

var registry = map[string]Driver{}

// Register makes a SQL engine available under its Name. Engine packages call
// it from init.
func Register(d Driver) { registry[d.Name()] = d }

// Get returns the SQL driver registered under name.
func Get(name string) (Driver, error) {
	d, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unsupported driver %q", name)
	}
	return d, nil
}

// Names lists every registered engine (SQL engines and stores), sorted.
func Names() []string {
	out := make([]string, 0, len(registry)+len(stores))
	for n := range registry {
		out = append(out, n)
	}
	for n := range stores {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Known reports whether name is a registered SQL engine or store.
func Known(name string) bool {
	_, a := registry[name]
	_, b := stores[name]
	return a || b
}

// QualifiedName returns schema.table quoted for the given driver.
func QualifiedName(d Driver, schema, table string) string {
	if schema == "" {
		return d.QuoteIdent(table)
	}
	return d.QuoteIdent(schema) + "." + d.QuoteIdent(table)
}

// QuoteWith wraps name in q, doubling any q inside it.
func QuoteWith(name, q string) string {
	return q + strings.ReplaceAll(name, q, q+q) + q
}

// IsWordByte reports whether b can be part of an unquoted SQL identifier.
func IsWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// NamedValues turns positional arguments into 1-based ordinal NamedValues,
// for database/sql drivers whose Stmt.Exec/Query delegate to the
// context-aware variants.
func NamedValues(args []sqldriver.Value) []sqldriver.NamedValue {
	out := make([]sqldriver.NamedValue, len(args))
	for i, a := range args {
		out[i] = sqldriver.NamedValue{Ordinal: i + 1, Value: a}
	}
	return out
}

// ScanStrings reads a single-string-column result and closes rows.
func ScanStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ScanForeignKeys reads rows of the seven ForeignKey fields, in struct
// order, and closes rows.
func ScanForeignKeys(rows *sql.Rows) ([]ForeignKey, error) {
	defer rows.Close()
	out := []ForeignKey{}
	for rows.Next() {
		var f ForeignKey
		if err := rows.Scan(&f.Name, &f.Schema, &f.Table, &f.Column, &f.RefSchema, &f.RefTable, &f.RefColumn); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ScanTables reads (schema, name, type) rows, normalizing the type to
// "table" or "view", and closes rows.
func ScanTables(rows *sql.Rows) ([]Table, error) {
	defer rows.Close()
	out := []Table{}
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Schema, &t.Name, &t.Type); err != nil {
			return nil, err
		}
		t.Type = normalizeTableType(t.Type)
		out = append(out, t)
	}
	return out, rows.Err()
}

func normalizeTableType(t string) string {
	if strings.Contains(strings.ToLower(t), "view") {
		return "view"
	}
	return "table"
}
