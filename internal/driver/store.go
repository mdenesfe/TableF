package driver

import "context"

// Store is an engine that is not SQL (MongoDB, Redis). The core browses it
// through StoreConn instead of building SQL.
type Store interface {
	Name() string
	Open(ctx context.Context, cfg Config) (StoreConn, error)
}

// StoreConn is an open connection to a Store.
type StoreConn interface {
	Close() error
	Features() Features
	Version(ctx context.Context) string
	// Databases lists the databases on the server (Redis: db0..db15).
	Databases(ctx context.Context) ([]string, error)
	// Tables lists collections (MongoDB) or key groups (Redis).
	Tables(ctx context.Context) ([]Table, error)
	// Columns describes a table; for schemaless engines it is inferred from a sample.
	Columns(ctx context.Context, table string) ([]Column, error)
	Rows(ctx context.Context, req RowsRequest) (RowsResult, error)
	// Split breaks an editor script into commands.
	Split(script string) []string
	// Execute runs one native command (a mongosh-style call, a Redis command).
	Execute(ctx context.Context, command string) (Result, error)
	// Apply writes edits made in the grid and returns a log of what ran.
	Apply(ctx context.Context, table string, ch Changes) ([]string, error)
}

// Match is an exact column = value condition (nil matches a missing value).
type Match struct {
	Column string `json:"column"`
	Value  any    `json:"value"`
}

// RowsRequest is a page of a store table.
type RowsRequest struct {
	Table    string
	Limit    int
	Offset   int
	OrderBy  string
	OrderDir string // asc | desc
	// Filter is what the user typed in the filter line, in the engine's own
	// syntax (a JSON filter for MongoDB, a key pattern for Redis).
	Filter string
	Match  []Match
}

// RowsResult is a page of rows.
type RowsResult struct {
	Columns []Column
	Rows    [][]any
	Total   int64
	// Query describes what ran, shown in the UI like the SQL of a table page.
	Query string
}

// Result is the outcome of one command.
type Result struct {
	Columns      []string
	Rows         [][]any
	RowsAffected int64
}

// RowChange sets Values on the row identified by Keys.
type RowChange struct {
	Keys   map[string]any
	Values map[string]any
}

// Changes is a batch of grid edits.
type Changes struct {
	Updates []RowChange
	Inserts []map[string]any
	Deletes []map[string]any
}

var stores = map[string]Store{}

// RegisterStore makes a non-SQL engine available under its Name.
func RegisterStore(s Store) { stores[s.Name()] = s }

// GetStore returns the store registered under name, if any.
func GetStore(name string) (Store, bool) {
	s, ok := stores[name]
	return s, ok
}
