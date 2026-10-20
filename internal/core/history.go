package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Query history: every script run in the editor is appended, as one JSON
// line, to history.jsonl next to the connections file. Appending keeps a run
// cheap however long the history is; the file is compacted to the newest
// historyKeep entries once it holds historyCompactAt.
const (
	historyKeep      = 10_000
	historyCompactAt = 12_500
	// historyMaxSQL caps the stored text of one script.
	historyMaxSQL = 64 << 10
	// DefaultHistoryLimit and MaxHistoryLimit bound one ListHistory call.
	DefaultHistoryLimit = 1000
	MaxHistoryLimit     = 10_000
)

// HistoryEntry is one script run.
type HistoryEntry struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connectionId"`
	Database     string `json:"database"`
	SQL          string `json:"sql"`
	// At is when the run started, in RFC 3339 (UTC).
	At         string  `json:"at"`
	DurationMs float64 `json:"durationMs"`
	// Rows is the row count of the last result (Partial when more rows
	// followed it), or the rows affected by the statements that change data.
	Rows    int64  `json:"rows"`
	Partial bool   `json:"partial,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Store) historyPath() string {
	return filepath.Join(filepath.Dir(s.path), "history.jsonl")
}

// loadHistory reads every entry, oldest first. A damaged line (a crash in the
// middle of a write) is skipped rather than failing the whole history.
func (s *Store) loadHistory() ([]HistoryEntry, error) {
	f, err := os.Open(s.historyPath())
	if errors.Is(err, fs.ErrNotExist) {
		return []HistoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []HistoryEntry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), historyMaxSQL*2)
	for sc.Scan() {
		var e HistoryEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// writeHistory replaces the file with entries (atomically).
func (s *Store) writeHistory(entries []HistoryEntry) error {
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return writeFileAtomic(s.historyPath(), []byte(b.String()))
}

// AddHistory appends one entry, compacting the file when it has grown.
func (s *Store) AddHistory(e HistoryEntry) error {
	if len(e.SQL) > historyMaxSQL {
		e.SQL = e.SQL[:historyMaxSQL]
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.historyPath()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.historyPath(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	s.histLines++
	if s.histLines < historyCompactAt {
		return nil
	}
	all, err := s.loadHistory()
	if err != nil {
		return err
	}
	s.histLines = len(all)
	if len(all) < historyCompactAt {
		return nil // the count was an estimate (another process, a restart)
	}
	all = all[len(all)-historyKeep:]
	s.histLines = len(all)
	return s.writeHistory(all)
}

// History returns entries newest first: those of connectionID ("" for all)
// whose SQL contains query (case-insensitive), at most limit.
func (s *Store) History(connectionID, query string, limit int) ([]HistoryEntry, error) {
	s.histMu.Lock()
	all, err := s.loadHistory()
	if err == nil {
		s.histLines = len(all)
	}
	s.histMu.Unlock()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := []HistoryEntry{}
	for _, e := range slices.Backward(all) {
		if len(out) >= limit {
			break
		}
		if connectionID != "" && e.ConnectionID != connectionID {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.SQL), q) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ClearHistory removes the entries of connectionID ("" for all).
func (s *Store) ClearHistory(connectionID string) error {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	all, err := s.loadHistory()
	if err != nil {
		return err
	}
	keep := all[:0]
	if connectionID != "" {
		for _, e := range all {
			if e.ConnectionID != connectionID {
				keep = append(keep, e)
			}
		}
	}
	s.histLines = len(keep)
	return s.writeHistory(keep)
}

// recordRun adds a script run to the history; a failure to write it never
// fails the query.
func (s *Service) recordRun(sess *session, script string, started time.Time, results []QueryResult) {
	e := HistoryEntry{
		ID:           newID(),
		ConnectionID: sess.cfg.ID,
		Database:     sess.current,
		SQL:          strings.TrimSpace(script),
		At:           started.UTC().Format(time.RFC3339),
		DurationMs:   float64(time.Since(started).Microseconds()) / 1000,
	}
	for _, r := range results {
		if r.Error != "" && e.Error == "" {
			e.Error = r.Error
		}
		if len(r.Columns) > 0 {
			e.Rows, e.Partial = int64(len(r.Rows)), r.Truncated
		} else {
			e.Rows += r.RowsAffected
		}
	}
	_ = s.store.AddHistory(e)
}

// ListHistory returns the query history of a connection ("" for every
// connection), newest first, optionally only runs whose SQL contains query.
func (s *Service) ListHistory(connectionID, query string, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	return s.store.History(connectionID, query, min(limit, MaxHistoryLimit))
}

// ClearHistory deletes the query history of a connection ("" for all).
func (s *Service) ClearHistory(connectionID string) error {
	return s.store.ClearHistory(connectionID)
}
