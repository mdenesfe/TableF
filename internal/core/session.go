package core

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
	"github.com/mdenesfe/TableF/internal/tunnel"
)

// metaTTL bounds how long cached table metadata is trusted, so DDL run by
// another client shows up without reconnecting.
const metaTTL = 2 * time.Minute

// session is one open connection.
type session struct {
	cfg   driver.Config
	drv   driver.Driver    // SQL engines
	db    *sql.DB          // SQL engines
	store driver.StoreConn // non-SQL engines (MongoDB, Redis)
	feat  driver.Features
	tun   *tunnel.Tunnel // nil without SSH

	mu      sync.Mutex
	closed  bool
	nextRun uint64
	running map[uint64]context.CancelFunc // ExecuteQuery calls in flight

	metaMu sync.Mutex
	meta   map[tableKey]tableMeta

	// current is the database the session is in; dial is the config used to
	// reach the server (through the SSH tunnel when there is one).
	current string
	dial    driver.Config
	// others are pooled connections to other databases on the same server,
	// opened when the Object Explorer expands them.
	othersMu sync.Mutex
	others   map[string]*sql.DB

	// lastUsed is when the session last served a call (UnixNano); a session
	// idle for longer than idleCheck is pinged before use.
	lastUsed atomic.Int64

	// cursors are query results the UI is still reading (see FetchRows).
	cursorsMu sync.Mutex
	cursors   map[string]*cursor
}

type tableKey struct{ schema, table string }

// tableMeta is what GetTableData needs besides the rows; it rarely changes.
type tableMeta struct {
	cols    []driver.Column
	out, in []driver.ForeignKey
	at      time.Time
}

// idleCheck is how long a session may sit unused before it is pinged before
// the next call; pings are skipped while it is busy.
const idleCheck = time.Minute

// healthy reports whether the session can serve a call: its SSH tunnel is up
// and, after idleCheck without use, the server still answers a ping.
func (s *session) healthy(parent context.Context) bool {
	if s.tun != nil && s.tun.Closed() {
		return false
	}
	now := time.Now().UnixNano()
	last := s.lastUsed.Swap(now)
	if last != 0 && time.Duration(now-last) < idleCheck {
		return true
	}
	if s.db == nil || s.busy() {
		// Store engines reconnect on their own; a busy session (a running
		// query, an open cursor holding SQLite's only connection) is alive.
		return true
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	return s.db.PingContext(ctx) == nil
}

// busy reports whether a query is running or a result is still being read.
func (s *session) busy() bool {
	s.mu.Lock()
	running := len(s.running)
	s.mu.Unlock()
	s.cursorsMu.Lock()
	defer s.cursorsMu.Unlock()
	return running > 0 || len(s.cursors) > 0
}

// startRun registers a cancelable query run; done must be called when it ends.
// On a closed session the returned context is already canceled.
func (s *session) startRun(parent context.Context) (ctx context.Context, done func()) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		cancel()
		return ctx, func() {}
	}
	if s.running == nil {
		s.running = map[uint64]context.CancelFunc{}
	}
	s.nextRun++
	id := s.nextRun
	s.running[id] = cancel
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}
}

// cancelRuns aborts every running query of the session.
func (s *session) cancelRuns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.running {
		cancel()
	}
}

// close cancels running queries and releases the connection. It may block on
// the network, so callers must not hold Service.mu.
func (s *session) close() {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.running {
		cancel()
	}
	s.mu.Unlock()
	s.closeCursors()
	if s.store != nil {
		s.store.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
	s.othersMu.Lock()
	for _, db := range s.others {
		db.Close()
	}
	s.others = nil
	s.othersMu.Unlock()
	if s.tun != nil {
		s.tun.Close()
	}
}

// tableMeta returns the columns and foreign keys of a table, cached per
// session. Foreign keys are a convenience: failing to read them is not an error.
func (s *session) tableMeta(ctx context.Context, schema, table string) (tableMeta, error) {
	key := tableKey{schema, table}
	s.metaMu.Lock()
	m, ok := s.meta[key]
	s.metaMu.Unlock()
	if ok && time.Since(m.at) < metaTTL {
		return m, nil
	}
	cols, err := s.drv.Columns(ctx, s.db, schema, table)
	if err != nil {
		return tableMeta{}, err
	}
	m = tableMeta{cols: cols, out: []driver.ForeignKey{}, in: []driver.ForeignKey{}, at: time.Now()}
	if s.feat.ForeignKeys {
		if out, in, err := s.drv.ForeignKeys(ctx, s.db, schema, table); err == nil {
			m.out, m.in = out, in
		}
	}
	s.metaMu.Lock()
	if s.meta == nil {
		s.meta = map[tableKey]tableMeta{}
	}
	s.meta[key] = m
	s.metaMu.Unlock()
	return m, nil
}

// invalidateMeta drops cached table metadata, e.g. after DDL.
func (s *session) invalidateMeta() {
	s.metaMu.Lock()
	s.meta = nil
	s.metaMu.Unlock()
}

// parallel reports whether independent queries may run at the same time;
// a single-connection pool (SQLite) would only queue them.
func (s *session) parallel() bool {
	return s.db.Stats().MaxOpenConnections != 1
}
