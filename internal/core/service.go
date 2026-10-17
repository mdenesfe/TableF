// Package core holds the application logic shared by the desktop and web builds.
// Every exported method on Service is callable from the frontend: by Wails
// bindings on desktop and through the RPC endpoint in web mode.
package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/mdenesfe/TableF/internal/driver"
	"github.com/mdenesfe/TableF/internal/tunnel"
)

// MaxQueryRows caps how many rows an ad-hoc query returns to the UI.
const MaxQueryRows = 5000

// state is shared by a Service and every view of it made by WithContext.
type state struct {
	store    *Store
	mu       sync.Mutex // guards sessions; never held while closing one
	sessions map[string]*session
}

// Service is the API surface of TableF.
type Service struct {
	*state
	// ctx bounds every call made through this value (see WithContext);
	// nil means context.Background().
	ctx context.Context
}

// NewService returns a Service that keeps saved connections in store.
func NewService(store *Store) *Service {
	return &Service{state: &state{store: store, sessions: map[string]*session{}}}
}

// WithContext returns a view of s whose calls stop when ctx is done, such as
// an HTTP request that was abandoned. It shares connections with s. It is a
// function rather than a method so it isn't exposed as a frontend binding.
func WithContext(s *Service, ctx context.Context) *Service {
	return &Service{state: s.state, ctx: ctx}
}

func (s *Service) context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// withTimeout derives a context for one call from the Service's context.
func (s *Service) withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.context(), d)
}

// Shutdown closes every open connection.
func (s *Service) Shutdown() {
	s.mu.Lock()
	all := slices.Collect(maps.Values(s.sessions))
	clear(s.sessions)
	s.mu.Unlock()
	for _, sess := range all {
		sess.close()
	}
}

// Engines lists the database engines this build can connect to. Some are
// optional (DuckDB needs cgo), so the UI offers only what is listed here.
func (s *Service) Engines() []string { return driver.Names() }

// ---------- saved connections ----------

// ListConnections returns the saved connections sorted by name, with their
// secrets removed (see driver.Config.Secrets).
func (s *Service) ListConnections() ([]driver.Config, error) {
	list, err := s.store.List()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	for i := range list {
		list[i] = redact(list[i])
	}
	return list, nil
}

// withStoredSecrets fills the secrets the UI left empty from the saved
// connection with the same ID, if there is one.
func (s *Service) withStoredSecrets(cfg driver.Config) (driver.Config, error) {
	cfg.Secrets = nil
	if cfg.ID == "" {
		return cfg, nil
	}
	stored, err := s.store.Get(cfg.ID)
	if errors.Is(err, errNotFound) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	return mergeSecrets(cfg, stored), nil
}

// SaveConnection inserts or updates a saved connection. Secrets left empty
// keep their saved value. The result is redacted like ListConnections.
func (s *Service) SaveConnection(cfg driver.Config) (driver.Config, error) {
	if !driver.Known(cfg.Driver) {
		return cfg, fmt.Errorf("unsupported driver %q", cfg.Driver)
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return cfg, errors.New("name is required")
	}
	cfg, err := s.withStoredSecrets(cfg)
	if err != nil {
		return redact(cfg), err
	}
	saved, err := s.store.Put(cfg)
	return redact(saved), err
}

// DeleteConnection closes and forgets a saved connection.
func (s *Service) DeleteConnection(id string) error {
	s.Disconnect(id)
	return s.store.Delete(id)
}

// TestResult reports what a successful test connection found.
type TestResult struct {
	Version   string  `json:"version"`
	LatencyMs float64 `json:"latencyMs"`
}

// TestConnection opens and pings a connection without saving it. When cfg.ID
// names a saved connection, secrets left empty are taken from it.
func (s *Service) TestConnection(cfg driver.Config) (TestResult, error) {
	cfg, err := s.withStoredSecrets(cfg)
	if err != nil {
		return TestResult{}, err
	}
	start := time.Now()
	sess, err := s.open(cfg)
	if err != nil {
		return TestResult{}, err
	}
	defer sess.close()
	latency := float64(time.Since(start).Microseconds()) / 1000
	ctx, cancel := s.withTimeout(5 * time.Second)
	defer cancel()
	return TestResult{Version: serverVersion(ctx, sess), LatencyMs: latency}, nil
}

// TrustHostKey records the host key of cfg's SSH server as trusted (after the
// user confirmed the fingerprint from an "unknown SSH host key" error) and
// returns its SHA256 fingerprint. A key that conflicts with a recorded one is
// refused.
func (s *Service) TrustHostKey(cfg driver.Config) (string, error) {
	return tunnel.TrustHostKey(cfg, s.store.KnownHostsPath())
}

// ---------- sessions ----------

// SessionInfo is returned after connecting.
type SessionInfo struct {
	ID        string   `json:"id"`
	Driver    string   `json:"driver"`
	Database  string   `json:"database"`
	Databases []string `json:"databases"`
	Version   string   `json:"version"`
	// Features tells the UI what this engine can do and which dialect it speaks.
	Features driver.Features `json:"features"`
}

// open connects (through an SSH tunnel when configured) and pings the server.
func (s *Service) open(cfg driver.Config) (*session, error) {
	sess := &session{cfg: cfg}
	var err error
	dial := cfg
	if cfg.SSHEnabled && driver.UsesNetwork(cfg.Driver) {
		if sess.tun, err = tunnel.Open(cfg, s.store.KnownHostsPath()); err != nil {
			return nil, err
		}
		dial.Host, dial.Port = "127.0.0.1", sess.tun.Port
	}
	sess.dial = dial
	if st, ok := driver.GetStore(cfg.Driver); ok {
		ctx, cancel := s.withTimeout(15 * time.Second)
		defer cancel()
		if sess.store, err = st.Open(ctx, dial); err != nil {
			sess.close()
			return nil, err
		}
		sess.feat = sess.store.Features()
		return sess, nil
	}
	if sess.drv, err = driver.Get(cfg.Driver); err != nil {
		sess.close()
		return nil, err
	}
	sess.feat = driver.FeaturesOf(sess.drv)
	_, sess.feat.Security = sess.drv.(driver.Securer)
	if sess.db, err = sess.drv.Open(dial); err != nil {
		sess.close()
		return nil, err
	}
	driver.ConfigurePool(sess.drv, sess.db)
	ctx, cancel := s.withTimeout(10 * time.Second)
	defer cancel()
	if err := sess.db.PingContext(ctx); err != nil {
		sess.close()
		return nil, err
	}
	return sess, nil
}

// Connect opens a saved connection. The session ID is the connection ID.
func (s *Service) Connect(id string) (SessionInfo, error) {
	cfg, err := s.store.Get(id)
	if err != nil {
		return SessionInfo{}, err
	}
	return s.connect(cfg)
}

func (s *Service) connect(cfg driver.Config) (SessionInfo, error) {
	sess, err := s.open(cfg)
	if err != nil {
		return SessionInfo{}, err
	}
	s.mu.Lock()
	old := s.sessions[cfg.ID]
	s.sessions[cfg.ID] = sess
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	info, err := s.info(sess)
	if err != nil {
		// Don't leave a session behind that the UI believes failed.
		s.mu.Lock()
		if s.sessions[cfg.ID] == sess {
			delete(s.sessions, cfg.ID)
		}
		s.mu.Unlock()
		sess.close()
		return SessionInfo{}, err
	}
	return info, nil
}

func (s *Service) info(sess *session) (SessionInfo, error) {
	ctx, cancel := s.withTimeout(15 * time.Second)
	defer cancel()
	var dbs []string
	var err error
	if sess.store != nil {
		dbs, err = sess.store.Databases(ctx)
	} else {
		dbs, err = sess.drv.Databases(ctx, sess.db)
	}
	if err != nil {
		return SessionInfo{}, err
	}
	if dbs == nil {
		dbs = []string{}
	}
	info := SessionInfo{ID: sess.cfg.ID, Driver: sess.cfg.Driver, Database: sess.cfg.Database, Databases: dbs, Features: sess.feat}
	if info.Database == "" {
		info.Database = currentDatabase(ctx, sess)
	}
	info.Version = serverVersion(ctx, sess)
	sess.current = info.Database
	return info, nil
}

func currentDatabase(ctx context.Context, sess *session) string {
	if sess.store != nil {
		return ""
	}
	if c, ok := sess.drv.(driver.CurrentDatabaser); ok {
		return c.CurrentDatabase(ctx, sess.db)
	}
	return ""
}

func serverVersion(ctx context.Context, sess *session) string {
	if sess.store != nil {
		return sess.store.Version(ctx)
	}
	if v, ok := sess.drv.(driver.Versioner); ok {
		return v.Version(ctx, sess.db)
	}
	return driver.QueryString(ctx, sess.db, "SELECT version()")
}

// Disconnect closes a session; unknown IDs are ignored.
func (s *Service) Disconnect(id string) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if ok {
		sess.close()
	}
}

// UseDatabase reconnects the session to another database on the same server.
func (s *Service) UseDatabase(id, database string) (SessionInfo, error) {
	sess, err := s.get(id)
	if err != nil {
		return SessionInfo{}, err
	}
	cfg := sess.cfg
	cfg.Database = database
	return s.connect(cfg)
}

func (s *Service) get(id string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, errors.New("not connected")
	}
	return sess, nil
}

// ---------- schema ----------

// ListTables lists the tables and views of the session's database. It also
// drops cached table metadata, so a refresh in the UI picks up outside DDL.
func (s *Service) ListTables(id string) ([]driver.Table, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	if sess.store != nil {
		return sess.store.Tables(ctx)
	}
	sess.invalidateMeta()
	return sess.drv.Tables(ctx, sess.db)
}

// GetColumns describes a table's columns.
func (s *Service) GetColumns(id, schema, table string) ([]driver.Column, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	if sess.store != nil {
		return sess.store.Columns(ctx, table)
	}
	if schema == "" {
		schema = sess.drv.DefaultSchema(sess.cfg)
	}
	return sess.drv.Columns(ctx, sess.db, schema, table)
}

// ListSchemas lists schemas, or nothing for engines without them.
func (s *Service) ListSchemas(id string) ([]string, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if !sess.feat.Schemas {
		return []string{}, nil
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	out, err := sess.drv.Schemas(ctx, sess.db)
	if out == nil {
		out = []string{}
	}
	return out, err
}

// CreateSchema creates an empty schema (PostgreSQL only).
func (s *Service) CreateSchema(id, name string) error {
	sess, err := s.get(id)
	if err != nil {
		return err
	}
	if !sess.feat.CreateSchema {
		return errors.New("this database doesn't support creating schemas")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("schema name is required")
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	_, err = sess.db.ExecContext(ctx, "CREATE SCHEMA "+sess.drv.QuoteIdent(name))
	return err
}

// ListRoutines lists stored functions and procedures.
func (s *Service) ListRoutines(id string) ([]driver.Routine, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if !sess.feat.Routines {
		return []driver.Routine{}, nil
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	return sess.drv.Routines(ctx, sess.db)
}

// GetRoutineDefinition returns the CREATE statement of a function or procedure.
func (s *Service) GetRoutineDefinition(id string, r driver.Routine) (string, error) {
	sess, err := s.get(id)
	if err != nil {
		return "", err
	}
	if !sess.feat.Routines {
		return "", errors.New("this database has no routines")
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	return sess.drv.RoutineDefinition(ctx, sess.db, r)
}

// ---------- data ----------

// QueryResult is a single statement's result.
type QueryResult struct {
	Statement    string   `json:"statement"`
	Columns      []string `json:"columns"`
	ColumnTypes  []string `json:"columnTypes"`
	Rows         [][]any  `json:"rows"`
	RowsAffected int64    `json:"rowsAffected"`
	DurationMs   float64  `json:"durationMs"`
	Truncated    bool     `json:"truncated"`
	// Cursor is set when more rows follow; FetchRows reads them.
	Cursor string `json:"cursor,omitempty"`
	Error  string `json:"error,omitempty"`
}

// TableDataRequest describes a page of table data.
type TableDataRequest struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
	OrderBy  string `json:"orderBy"`
	OrderDir string `json:"orderDir"`
	Where    string `json:"where"` // raw SQL condition typed by the user
	// OrderSQL is a raw ORDER BY list typed by the user; it wins over OrderBy.
	OrderSQL string `json:"orderSql"`
	// Match narrows rows to exact column values (NULL matches IS NULL), e.g. an open group.
	Match []ColumnMatch `json:"match"`
}

// ColumnMatch is an exact, parameterized column = value condition.
type ColumnMatch struct {
	Column string `json:"column"`
	Value  any    `json:"value"`
}

// filterSQL combines the user's raw condition with parameterized matches.
// Placeholders are numbered from firstArg.
func filterSQL(d driver.Driver, where string, match []ColumnMatch, firstArg int) (string, []any) {
	var parts []string
	var args []any
	if w := strings.TrimSpace(where); w != "" {
		parts = append(parts, "("+w+")")
	}
	for _, m := range match {
		if m.Value == nil {
			parts = append(parts, d.QuoteIdent(m.Column)+" IS NULL")
			continue
		}
		args = append(args, m.Value)
		parts = append(parts, d.QuoteIdent(m.Column)+" = "+d.Placeholder(firstArg+len(args)-1))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

// TableData is a page of rows plus the table's columns and total row count.
type TableData struct {
	Columns    []driver.Column `json:"columns"`
	Rows       [][]any         `json:"rows"`
	Total      int64           `json:"total"`
	SQL        string          `json:"sql"`
	DurationMs float64         `json:"durationMs"`
	// ForeignKeys are this table's outgoing references; ReferencedBy point at it.
	ForeignKeys  []driver.ForeignKey `json:"foreignKeys"`
	ReferencedBy []driver.ForeignKey `json:"referencedBy"`
}

// GetTableData loads a page of a table with its total row count. The page,
// the count and the (cached) table metadata are fetched concurrently unless
// the engine has a single connection.
func (s *Service) GetTableData(id string, req TableDataRequest) (TableData, error) {
	sess, err := s.get(id)
	if err != nil {
		return TableData{}, err
	}
	if req.Limit <= 0 || req.Limit > 10000 {
		req.Limit = 300
	}
	ctx, cancel := s.withTimeout(60 * time.Second)
	defer cancel()
	if sess.store != nil {
		return storeTableData(ctx, sess.store, req)
	}
	d := sess.drv
	if req.Schema == "" {
		req.Schema = d.DefaultSchema(sess.cfg)
	}

	from := driver.QualifiedName(d, req.Schema, req.Table)
	where, args := filterSQL(d, req.Where, req.Match, 1)
	order := ""
	if o := strings.TrimSpace(req.OrderSQL); o != "" {
		order = " ORDER BY " + o
	} else if req.OrderBy != "" {
		dir := "ASC"
		if strings.EqualFold(req.OrderDir, "desc") {
			dir = "DESC"
		}
		order = " ORDER BY " + d.QuoteIdent(req.OrderBy) + " " + dir
	}
	q, skip := driver.Paginate(d, fmt.Sprintf("SELECT * FROM %s%s%s", from, where, order), req.Limit, req.Offset)

	var (
		meta    tableMeta
		data    [][]any
		total   int64
		elapsed float64
	)
	g, gctx := errgroup.WithContext(ctx)
	if !sess.parallel() {
		g.SetLimit(1)
	}
	g.Go(func() (err error) {
		meta, err = sess.tableMeta(gctx, req.Schema, req.Table)
		return err
	})
	g.Go(func() error {
		start := time.Now()
		rows, err := sess.db.QueryContext(gctx, q, args...)
		if err != nil {
			return err
		}
		_, data, _, err = readRows(rows, req.Limit, skip)
		elapsed = float64(time.Since(start).Microseconds()) / 1000
		return err
	})
	g.Go(func() error {
		return sess.db.QueryRowContext(gctx, "SELECT COUNT(*) FROM "+from+where, args...).Scan(&total)
	})
	if err := g.Wait(); err != nil {
		return TableData{}, err
	}
	return TableData{Columns: meta.cols, Rows: data, Total: total, SQL: q, DurationMs: elapsed, ForeignKeys: meta.out, ReferencedBy: meta.in}, nil
}

// GroupRequest asks for the distinct values of Column with their row counts,
// inside the rows selected by Where and the already-open parent groups (Match).
type GroupRequest struct {
	Schema string        `json:"schema"`
	Table  string        `json:"table"`
	Where  string        `json:"where"`
	Match  []ColumnMatch `json:"match"`
	Column string        `json:"column"`
	Limit  int           `json:"limit"`
}

// GroupCount is one group: a distinct value and how many rows have it.
type GroupCount struct {
	Value any   `json:"value"`
	Count int64 `json:"count"`
}

// GroupResult lists groups, largest first, with the empty (NULL) group last.
type GroupResult struct {
	Groups []GroupCount `json:"groups"`
	Total  int64        `json:"total"` // number of distinct groups
	Rows   int64        `json:"rows"`  // rows across all groups
}

// GetGroups returns the distinct values of a column with their row counts.
func (s *Service) GetGroups(id string, req GroupRequest) (GroupResult, error) {
	sess, err := s.get(id)
	if err != nil {
		return GroupResult{}, err
	}
	if !sess.feat.Groups || sess.store != nil {
		return GroupResult{}, errors.New("grouping isn't supported for this database")
	}
	d := sess.drv
	if req.Schema == "" {
		req.Schema = d.DefaultSchema(sess.cfg)
	}
	if req.Limit <= 0 || req.Limit > 5000 {
		req.Limit = 500
	}
	ctx, cancel := s.withTimeout(60 * time.Second)
	defer cancel()

	from := driver.QualifiedName(d, req.Schema, req.Table)
	where, args := filterSQL(d, req.Where, req.Match, 1)
	col := d.QuoteIdent(req.Column)
	q, _ := driver.Paginate(d, fmt.Sprintf(
		"SELECT %s, COUNT(*) AS n FROM %s%s GROUP BY %s ORDER BY CASE WHEN %s IS NULL THEN 1 ELSE 0 END, n DESC, %s",
		col, from, where, col, col, col), req.Limit, 0)
	rows, err := sess.db.QueryContext(ctx, q, args...)
	if err != nil {
		return GroupResult{}, err
	}
	defer rows.Close()
	out := GroupResult{Groups: []GroupCount{}}
	for rows.Next() {
		var g GroupCount
		var v any
		if err := rows.Scan(&v, &g.Count); err != nil {
			return GroupResult{}, err
		}
		g.Value = normalize(v)
		out.Groups = append(out.Groups, g)
	}
	if err := rows.Err(); err != nil {
		return GroupResult{}, err
	}
	err = sess.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*), COALESCE(SUM(n), 0) FROM (SELECT COUNT(*) AS n FROM %s%s GROUP BY %s) g", from, where, col),
		args...).Scan(&out.Total, &out.Rows)
	return out, err
}

// ExecuteQuery runs every statement in script and returns one result each.
// Execution stops at the first failing statement. Several calls may run on
// one session at once; CancelQuery stops them all.
func (s *Service) ExecuteQuery(id, script string) ([]QueryResult, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	var stmts []string
	if sess.store != nil {
		stmts = sess.store.Split(script)
	} else {
		stmts = SplitStatements(script, sess.feat.Family)
	}
	if len(stmts) == 0 {
		return nil, errors.New("nothing to execute")
	}

	ctx, done := sess.startRun(s.context())
	defer done()

	if sess.store != nil {
		return storeExecute(ctx, sess.store, stmts), nil
	}

	// A dedicated connection keeps session state (SET, temp tables, USE) between statements.
	conn, err := sess.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// The connection stays open when the last result is left as a cursor.
	keepConn := false
	defer func() {
		if !keepConn {
			conn.Close()
		}
	}()

	// Statements that may be DDL invalidate cached columns and keys once the
	// script is done (even when it failed half-way).
	if slices.ContainsFunc(stmts, func(st string) bool { return !readOnly(st) }) {
		defer sess.invalidateMeta()
	}
	results := make([]QueryResult, 0, len(stmts))
	for i, stmt := range stmts {
		res := QueryResult{Statement: stmt, Columns: []string{}, ColumnTypes: []string{}, Rows: [][]any{}}
		start := time.Now()
		if returnsRows(stmt) && i == len(stmts)-1 {
			// The last result can stay open: its first rows come back now and
			// the UI reads the rest in chunks (FetchRows).
			var cur string
			res.Columns, res.ColumnTypes, res.Rows, cur, err = s.queryWithCursor(sess, conn, stmt)
			if cur != "" {
				res.Cursor, res.Truncated, keepConn = cur, true, true
			}
		} else if returnsRows(stmt) {
			rows, qerr := conn.QueryContext(ctx, stmt)
			if qerr == nil {
				res.ColumnTypes = columnTypes(rows)
				res.Columns, res.Rows, res.Truncated, qerr = readRows(rows, MaxQueryRows, 0)
			}
			err = qerr
		} else {
			r, xerr := conn.ExecContext(ctx, stmt)
			if xerr == nil {
				res.RowsAffected, _ = r.RowsAffected()
			}
			err = xerr
		}
		res.DurationMs = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			break
		}
		results = append(results, res)
	}
	return results, nil
}

// CancelQuery aborts every running ExecuteQuery of a session.
func (s *Service) CancelQuery(id string) error {
	sess, err := s.get(id)
	if err != nil {
		return err
	}
	sess.cancelRuns()
	return nil
}

// ---------- editing ----------

// RowUpdate changes Values on the row identified by Keys.
type RowUpdate struct {
	Keys   map[string]any `json:"keys"`
	Values map[string]any `json:"values"`
}

// ChangeSet is a batch of edits applied in one transaction.
type ChangeSet struct {
	Schema  string           `json:"schema"`
	Table   string           `json:"table"`
	Updates []RowUpdate      `json:"updates"`
	Inserts []map[string]any `json:"inserts"`
	Deletes []map[string]any `json:"deletes"` // key columns of each deleted row
}

// ApplyChanges commits a ChangeSet atomically and returns the statements run.
// Engines without transactions apply the edits one by one, and before each
// UPDATE or DELETE check that its key matches exactly one row.
func (s *Service) ApplyChanges(id string, cs ChangeSet) ([]string, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if !sess.feat.Edit {
		return nil, errors.New("editing isn't supported for this database")
	}
	ctx, cancel := s.withTimeout(60 * time.Second)
	defer cancel()
	if sess.store != nil {
		return sess.store.Apply(ctx, cs.Table, driver.Changes{Updates: rowChanges(cs.Updates), Inserts: cs.Inserts, Deletes: cs.Deletes})
	}
	d := sess.drv
	if cs.Schema == "" {
		cs.Schema = d.DefaultSchema(sess.cfg)
	}
	table := driver.QualifiedName(d, cs.Schema, cs.Table)

	var ex interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	} = sess.db
	var tx *sql.Tx
	if sess.feat.Transactions {
		var err error
		if tx, err = sess.db.BeginTx(ctx, nil); err != nil {
			return nil, err
		}
		defer tx.Rollback()
		ex = tx
	}

	// uniqueRow guards edits that can't be rolled back: the keys must match
	// exactly one row before the statement runs.
	uniqueRow := func(keys map[string]any, q string) error {
		if tx != nil {
			return nil
		}
		where, args := whereClause(d, keys, 1)
		var n int64
		if err := sess.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&n); err != nil {
			return fmt.Errorf("%w\n%s", err, q)
		}
		if n != 1 {
			return fmt.Errorf("refusing to run: the row's key matches %d rows, expected 1 — table may lack a unique key\n%s", n, q)
		}
		return nil
	}

	var log []string
	exec := func(q string, args []any, wantOne bool) error {
		r, err := ex.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("%w\n%s", err, q)
		}
		if wantOne {
			if n, err := r.RowsAffected(); err == nil && n != 1 {
				return fmt.Errorf("expected to change 1 row, changed %d — table may lack a unique key\n%s", n, q)
			}
		}
		log = append(log, q)
		return nil
	}

	for _, keys := range cs.Deletes {
		where, args := whereClause(d, keys, 1)
		q := "DELETE FROM " + table + " WHERE " + where
		if err := uniqueRow(keys, q); err != nil {
			return nil, err
		}
		if err := exec(q, args, true); err != nil {
			return nil, err
		}
	}
	for _, u := range cs.Updates {
		if len(u.Values) == 0 {
			continue
		}
		cols := slices.Sorted(maps.Keys(u.Values))
		sets := make([]string, len(cols))
		args := make([]any, 0, len(cols)+len(u.Keys))
		for i, c := range cols {
			sets[i] = d.QuoteIdent(c) + " = " + d.Placeholder(i+1)
			args = append(args, u.Values[c])
		}
		where, wargs := whereClause(d, u.Keys, len(cols)+1)
		q := "UPDATE " + table + " SET " + strings.Join(sets, ", ") + " WHERE " + where
		if err := uniqueRow(u.Keys, q); err != nil {
			return nil, err
		}
		if err := exec(q, append(args, wargs...), true); err != nil {
			return nil, err
		}
	}
	for _, row := range cs.Inserts {
		cols := slices.Sorted(maps.Keys(row))
		if len(cols) == 0 {
			q := "INSERT INTO " + table + " DEFAULT VALUES"
			if e, ok := d.(driver.EmptyInserter); ok {
				q = e.EmptyInsert(table)
			}
			if err := exec(q, nil, false); err != nil {
				return nil, err
			}
			continue
		}
		names := make([]string, len(cols))
		ph := make([]string, len(cols))
		args := make([]any, len(cols))
		for i, c := range cols {
			names[i] = d.QuoteIdent(c)
			ph[i] = d.Placeholder(i + 1)
			args[i] = row[c]
		}
		q := "INSERT INTO " + table + " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(ph, ", ") + ")"
		if err := exec(q, args, false); err != nil {
			return nil, err
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return log, nil
}

// whereClause matches every key column (NULL with IS NULL), numbering
// placeholders from startArg.
func whereClause(d driver.Driver, keys map[string]any, startArg int) (string, []any) {
	cols := slices.Sorted(maps.Keys(keys))
	parts := make([]string, len(cols))
	args := []any{}
	n := startArg
	for i, c := range cols {
		if keys[c] == nil {
			parts[i] = d.QuoteIdent(c) + " IS NULL"
			continue
		}
		parts[i] = d.QuoteIdent(c) + " = " + d.Placeholder(n)
		args = append(args, keys[c])
		n++
	}
	if len(parts) == 0 {
		return "1 = 0", nil // never touch every row by accident
	}
	return strings.Join(parts, " AND "), args
}
