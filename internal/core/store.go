package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/mdenesfe/TableF/internal/driver"
)

// errNotFound is returned by Store.Get for an unknown ID.
var errNotFound = errors.New("connection not found")

// Store persists saved connections as a JSON file in the user config dir.
// The file holds secrets, so it is written with mode 0600.
type Store struct {
	mu   sync.Mutex
	path string
}

// DefaultStorePath returns <user config dir>/TableF/connections.json.
func DefaultStorePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "TableF", "connections.json"), nil
}

// NewStore returns a Store backed by the JSON file at path.
func NewStore(path string) *Store { return &Store{path: path} }

// KnownHostsPath is where SSH host keys trusted on first use are recorded.
func (s *Store) KnownHostsPath() string {
	return filepath.Join(filepath.Dir(s.path), "known_hosts")
}

func (s *Store) load() ([]driver.Config, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return []driver.Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []driver.Config
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// save replaces the file atomically: the list is written to a temporary file
// that is synced to disk before it is renamed over the old one, so a crash
// leaves either the old or the new list, never a truncated one.
func (s *Store) save(list []driver.Config) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	for i := range list {
		list[i].Secrets = nil // UI-only; never persisted
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns every saved connection, secrets included.
func (s *Store) List() ([]driver.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Get returns the saved connection with the given ID, secrets included.
func (s *Store) Get(id string) (driver.Config, error) {
	list, err := s.List()
	if err != nil {
		return driver.Config{}, err
	}
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return driver.Config{}, errNotFound
}

// Put inserts or replaces a connection, assigning an ID when missing.
func (s *Store) Put(cfg driver.Config) (driver.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return cfg, err
	}
	if cfg.ID == "" {
		cfg.ID = newID()
		list = append(list, cfg)
	} else {
		found := false
		for i := range list {
			if list[i].ID == cfg.ID {
				list[i] = cfg
				found = true
			}
		}
		if !found {
			list = append(list, cfg)
		}
	}
	return cfg, s.save(list)
}

// Delete removes the saved connection with the given ID.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, c := range list {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return s.save(out)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
