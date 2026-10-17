package core

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Catalog is what the Object Explorer shows for one database.
type Catalog struct {
	Database string           `json:"database"`
	Tables   []driver.Table   `json:"tables"` // tables and views
	Routines []driver.Routine `json:"routines"`
	Schemas  []string         `json:"schemas"`
	// Security reports whether users and roles can be listed (ListPrincipals).
	Security bool `json:"security"`
}

// errOtherDatabase is returned for engines that can only browse the database
// the session is in (the UI switches to the database instead).
var errOtherDatabase = errors.New("switch to this database to browse it")

// dbFor returns a connection to database on the session's server: the session's
// own for the current database, else a pooled one opened on first use.
func (sess *session) dbFor(ctx context.Context, database string) (*sql.DB, error) {
	if database == "" || database == sess.current {
		return sess.db, nil
	}
	sess.othersMu.Lock()
	defer sess.othersMu.Unlock()
	if db, ok := sess.others[database]; ok {
		return db, nil
	}
	cfg := sess.dial
	cfg.Database = database
	db, err := sess.drv.Open(cfg)
	if err != nil {
		return nil, err
	}
	// Browsing needs little: keep these pools small.
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(2 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if sess.others == nil {
		sess.others = map[string]*sql.DB{}
	}
	sess.others[database] = db
	return db, nil
}

// GetCatalog lists the tables, views, routines and schemas of a database on
// the connected server, for the Object Explorer. An empty database means the
// current one.
func (s *Service) GetCatalog(id, database string) (Catalog, error) {
	sess, err := s.get(id)
	if err != nil {
		return Catalog{}, err
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	out := Catalog{Database: database, Tables: []driver.Table{}, Routines: []driver.Routine{}, Schemas: []string{}}
	if sess.store != nil {
		if database != "" && database != sess.current {
			return Catalog{}, errOtherDatabase
		}
		out.Tables, err = sess.store.Tables(ctx)
		return out, err
	}
	db, err := sess.dbFor(ctx, database)
	if err != nil {
		return Catalog{}, err
	}
	if out.Tables, err = sess.drv.Tables(ctx, db); err != nil {
		return Catalog{}, err
	}
	// Routines and schemas are extras: a database that hides them still browses.
	if sess.feat.Routines {
		if r, err := sess.drv.Routines(ctx, db); err == nil {
			out.Routines = r
		}
	}
	if sess.feat.Schemas {
		if sc, err := sess.drv.Schemas(ctx, db); err == nil && sc != nil {
			out.Schemas = sc
		}
	}
	_, out.Security = sess.drv.(driver.Securer)
	return out, nil
}

// ListPrincipals lists the users and roles of a database (Security in the
// Object Explorer). An empty database means the current one.
func (s *Service) ListPrincipals(id, database string) ([]driver.Principal, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	sec, ok := sess.drv.(driver.Securer)
	if sess.store != nil || !ok {
		return []driver.Principal{}, nil
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	db, err := sess.dbFor(ctx, database)
	if err != nil {
		return nil, err
	}
	return sec.Principals(ctx, db)
}

// GetTableInfo describes a table's storage and metadata (sizes, row
// estimate, comment…). Engines without driver.TableInformer return no fields.
func (s *Service) GetTableInfo(id, schema, table string) ([]driver.InfoField, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	inf, ok := sess.drv.(driver.TableInformer)
	if sess.store != nil || !ok {
		return []driver.InfoField{}, nil
	}
	if schema == "" {
		schema = sess.drv.DefaultSchema(sess.cfg)
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	return inf.TableInfo(ctx, sess.db, schema, table)
}

// GetIndexes lists a table's indexes. Engines without driver.Indexer return none.
func (s *Service) GetIndexes(id, schema, table string) ([]driver.Index, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	ix, ok := sess.drv.(driver.Indexer)
	if sess.store != nil || !ok {
		return []driver.Index{}, nil
	}
	if schema == "" {
		schema = sess.drv.DefaultSchema(sess.cfg)
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	return ix.Indexes(ctx, sess.db, schema, table)
}

// TableRelations are a table's foreign keys in both directions.
type TableRelations struct {
	ForeignKeys  []driver.ForeignKey `json:"foreignKeys"`
	ReferencedBy []driver.ForeignKey `json:"referencedBy"`
}

// GetRelations returns a table's outgoing references and the references
// pointing at it (cached with its columns), for query results that come
// from one table.
func (s *Service) GetRelations(id, schema, table string) (TableRelations, error) {
	empty := TableRelations{ForeignKeys: []driver.ForeignKey{}, ReferencedBy: []driver.ForeignKey{}}
	sess, err := s.get(id)
	if err != nil {
		return empty, err
	}
	if sess.store != nil || !sess.feat.ForeignKeys {
		return empty, nil
	}
	if schema == "" {
		schema = sess.drv.DefaultSchema(sess.cfg)
	}
	ctx, cancel := s.withTimeout(30 * time.Second)
	defer cancel()
	meta, err := sess.tableMeta(ctx, schema, table)
	if err != nil {
		return empty, err
	}
	return TableRelations{ForeignKeys: meta.out, ReferencedBy: meta.in}, nil
}
