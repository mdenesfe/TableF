package core

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SavedQuery is a named SQL script the user kept. Queries belong to the
// connection they were saved from; an empty ConnectionID means every
// connection lists it.
type SavedQuery struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SQL          string `json:"sql"`
	ConnectionID string `json:"connectionId"`
	// Updated is the last save time, in RFC 3339.
	Updated string `json:"updated"`
}

// queriesPath is queries.json next to the connections file.
func (s *Store) queriesPath() string {
	return filepath.Join(filepath.Dir(s.path), "queries.json")
}

func (s *Store) loadQueries() ([]SavedQuery, error) {
	b, err := os.ReadFile(s.queriesPath())
	if errors.Is(err, fs.ErrNotExist) {
		return []SavedQuery{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []SavedQuery{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Queries returns every saved query.
func (s *Store) Queries() ([]SavedQuery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadQueries()
}

// PutQuery inserts or replaces a saved query, assigning an ID when missing
// and stamping the save time.
func (s *Store) PutQuery(q SavedQuery) (SavedQuery, error) {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		return q, errors.New("a saved query needs a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadQueries()
	if err != nil {
		return q, err
	}
	q.Updated = time.Now().UTC().Format(time.RFC3339)
	found := false
	if q.ID != "" {
		for i := range list {
			if list[i].ID == q.ID {
				list[i] = q
				found = true
			}
		}
	} else {
		q.ID = newID()
	}
	if !found {
		list = append(list, q)
	}
	return q, writeJSON(s.queriesPath(), list)
}

// DeleteQuery removes the saved query with the given ID.
func (s *Store) DeleteQuery(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadQueries()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, q := range list {
		if q.ID != id {
			out = append(out, q)
		}
	}
	return writeJSON(s.queriesPath(), out)
}

// ListSavedQueries returns every saved query; the UI shows the ones of the
// current connection plus the shared ones.
func (s *Service) ListSavedQueries() ([]SavedQuery, error) { return s.store.Queries() }

// SaveQuery creates or updates a saved query and returns it as stored.
func (s *Service) SaveQuery(q SavedQuery) (SavedQuery, error) { return s.store.PutQuery(q) }

// DeleteSavedQuery removes a saved query.
func (s *Service) DeleteSavedQuery(id string) error { return s.store.DeleteQuery(id) }
