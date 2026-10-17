package core

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// The rest of a large query result stays open on the server and the UI reads
// it in chunks with FetchRows, so a query can return every row without one
// huge response.
const (
	// DefaultFetchRows and MaxFetchRows bound one FetchRows chunk.
	DefaultFetchRows = 10_000
	MaxFetchRows     = 50_000
	// cursorIdle closes a cursor nobody has read for this long (a closed tab,
	// a crashed page), releasing its connection.
	cursorIdle = 2 * time.Minute
)

var errNoCursor = errors.New("the rest of this result is no longer available; run the query again")

// cursor is an open result set positioned on its next, unscanned row.
type cursor struct {
	mu    sync.Mutex
	conn  *sql.Conn
	rows  *sql.Rows
	ncols int
	done  func() // ends the run: cancels its context
	idle  *time.Timer
}

func (c *cursor) close() {
	c.idle.Stop()
	c.rows.Close()
	c.conn.Close()
	c.done()
}

// RowPage is one chunk of a query result.
type RowPage struct {
	Rows [][]any `json:"rows"`
	// Done is true when the result has no more rows (the cursor is closed).
	Done bool `json:"done"`
}

// addCursor keeps rows (and the connection it reads from) open and returns
// the id the UI fetches the rest with.
func (s *session) addCursor(conn *sql.Conn, rows *sql.Rows, ncols int, done func()) string {
	s.cursorsMu.Lock()
	defer s.cursorsMu.Unlock()
	if s.cursors == nil {
		s.cursors = map[string]*cursor{}
	}
	id := newID()
	c := &cursor{conn: conn, rows: rows, ncols: ncols, done: done}
	c.idle = time.AfterFunc(cursorIdle, func() { s.dropCursor(id) })
	s.cursors[id] = c
	return id
}

// takeCursor removes a cursor from the session; the caller closes it.
func (s *session) takeCursor(id string) *cursor {
	s.cursorsMu.Lock()
	defer s.cursorsMu.Unlock()
	c := s.cursors[id]
	delete(s.cursors, id)
	return c
}

func (s *session) dropCursor(id string) {
	if c := s.takeCursor(id); c != nil {
		c.mu.Lock()
		c.close()
		c.mu.Unlock()
	}
}

// closeCursors closes every open cursor (the session is closing).
func (s *session) closeCursors() {
	s.cursorsMu.Lock()
	all := s.cursors
	s.cursors = nil
	s.cursorsMu.Unlock()
	for _, c := range all {
		c.mu.Lock()
		c.close()
		c.mu.Unlock()
	}
}

// FetchRows reads the next chunk of a query result that ExecuteQuery left
// open (QueryResult.Cursor). The cursor closes itself after the last row.
func (s *Service) FetchRows(id, cursorID string, limit int) (RowPage, error) {
	sess, err := s.get(id)
	if err != nil {
		return RowPage{}, err
	}
	if limit <= 0 {
		limit = DefaultFetchRows
	}
	limit = min(limit, MaxFetchRows)
	sess.cursorsMu.Lock()
	c := sess.cursors[cursorID]
	sess.cursorsMu.Unlock()
	if c == nil {
		return RowPage{}, errNoCursor
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.idle.Reset(cursorIdle)
	out, more, err := scanRows(c.rows, c.ncols, limit, true)
	if err != nil || !more {
		if sess.takeCursor(cursorID) == c {
			c.close()
		}
	}
	if err != nil {
		return RowPage{}, err
	}
	return RowPage{Rows: out, Done: !more}, nil
}

// CloseCursor drops the rest of a query result (the user stopped loading it
// or ran another query).
func (s *Service) CloseCursor(id, cursorID string) error {
	sess, err := s.get(id)
	if err != nil {
		return err
	}
	sess.dropCursor(cursorID)
	return nil
}

// openCursorRun starts a run for a result that may outlive this call (an HTTP
// request, for one). CancelQuery and closing the session end it like any
// query; the call's own context only stops it until stop is called, once
// the first rows are back.
func (s *Service) openCursorRun(sess *session) (ctx context.Context, done func(), stop func() bool) {
	ctx, done = sess.startRun(context.WithoutCancel(s.context()))
	return ctx, done, context.AfterFunc(s.context(), done)
}

// queryWithCursor runs a row-returning statement and reads its first
// MaxQueryRows rows. When more follow, the result stays open as a cursor (it
// then owns conn) and its id is returned.
func (s *Service) queryWithCursor(sess *session, conn *sql.Conn, stmt string) (cols, types []string, out [][]any, cursorID string, err error) {
	ctx, done, stop := s.openCursorRun(sess)
	defer stop() // the cursor no longer depends on this call once it returns
	rows, err := conn.QueryContext(ctx, stmt)
	if err != nil {
		done()
		return nil, nil, nil, "", err
	}
	types = columnTypes(rows)
	if cols, err = rows.Columns(); err != nil {
		rows.Close()
		done()
		return nil, nil, nil, "", err
	}
	out, more, err := scanRows(rows, len(cols), MaxQueryRows, false)
	if err != nil || !more {
		rows.Close()
		done()
		return cols, types, out, "", err
	}
	return cols, types, out, sess.addCursor(conn, rows, len(cols), done), nil
}
