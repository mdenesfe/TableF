package driver

import (
	"context"
	"database/sql"
)

// Engines that speak the PostgreSQL or MySQL wire protocol reuse those
// drivers under their own name.

type pgCompatible struct {
	postgres
	name string
	port int
	f    Features
}

func (p pgCompatible) Name() string       { return p.name }
func (p pgCompatible) Features() Features { return p.f }
func (p pgCompatible) Open(cfg Config) (*sql.DB, error) {
	if cfg.Port == 0 {
		cfg.Port = p.port
	}
	if cfg.Database == "" && p.name == "cockroach" {
		cfg.Database = "defaultdb"
	}
	if cfg.Database == "" && p.name == "redshift" {
		cfg.Database = "dev"
	}
	return p.postgres.Open(cfg)
}

// Columns: Redshift and CockroachDB emulate pg_index only partially, so they
// use the information_schema query; Greenplum is PostgreSQL underneath.
func (p pgCompatible) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	if p.name == "greenplum" {
		return p.postgres.Columns(ctx, db, schema, table)
	}
	return portableColumns(ctx, db, schema, table)
}

// Redshift has no pg_proc arguments helpers; skip routines.
func (p pgCompatible) Routines(ctx context.Context, db *sql.DB) ([]Routine, error) {
	if !p.f.Routines {
		return []Routine{}, nil
	}
	return p.postgres.Routines(ctx, db)
}

// myCompatible inherits Features and EmptyInsert from mysqlDriver.
type myCompatible struct {
	mysqlDriver
	name string
	port int
}

func (m myCompatible) Name() string { return m.name }
func (m myCompatible) Open(cfg Config) (*sql.DB, error) {
	if cfg.Port == 0 {
		cfg.Port = m.port
	}
	return m.mysqlDriver.Open(cfg)
}

func init() {
	pg := postgres{}.Features()
	redshift := pg
	redshift.Routines, redshift.ForeignKeys = false, false
	cockroach := pg
	cockroach.Routines = false
	Register(pgCompatible{name: "redshift", port: 5439, f: redshift})
	Register(pgCompatible{name: "cockroach", port: 26257, f: cockroach})
	Register(pgCompatible{name: "greenplum", port: 5432, f: pg})
	Register(myCompatible{name: "mariadb", port: 3306})
	Register(myCompatible{name: "singlestore", port: 3306})
}

// Features of the built-in engines.
func (postgres) Features() Features {
	return Features{Family: "postgres", Schemas: true, CreateSchema: true, Routines: true, ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}
func (mysqlDriver) Features() Features {
	return Features{Family: "mysql", Routines: true, ForeignKeys: true, Groups: true, Edit: true, Transactions: true}
}

// CurrentDatabase implements CurrentDatabaser.
func (postgres) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	return QueryString(ctx, db, "SELECT current_database()")
}

// CurrentDatabase implements CurrentDatabaser.
func (mysqlDriver) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	return QueryString(ctx, db, "SELECT COALESCE(DATABASE(), '')")
}

// EmptyInsert implements EmptyInserter; MySQL has no DEFAULT VALUES.
func (mysqlDriver) EmptyInsert(table string) string { return "INSERT INTO " + table + " () VALUES ()" }

// QueryString runs a query returning one string, "" on any error.
func QueryString(ctx context.Context, db *sql.DB, q string) string {
	var v sql.NullString
	if err := db.QueryRowContext(ctx, q).Scan(&v); err != nil {
		return ""
	}
	return v.String
}
