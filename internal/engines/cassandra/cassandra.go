// Package cassandra adds Apache Cassandra (and compatible servers such as
// ScyllaDB) as a SQL engine. CQL is spoken through gocql behind a small
// database/sql driver registered as "tablef-cassandra".
package cassandra

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/mdenesfe/TableF/internal/driver"
)

const defaultPort = 9042

type engine struct {
	driver.NoForeignKeys
	driver.NoRoutines
}

func init() { driver.Register(engine{}) }

func (engine) Name() string { return "cassandra" }

// Features: CQL has no joins, foreign keys, routines worth browsing or
// multi-statement transactions. GROUP BY is only allowed on primary key
// columns, so the generic grouping UI is disabled. UPDATE/DELETE by the full
// primary key and INSERT (an upsert) work, so editing is enabled.
func (engine) Features() driver.Features {
	return driver.Features{Family: "cassandra", Schemas: true, Edit: true}
}

func (engine) Open(cfg driver.Config) (*sql.DB, error) {
	c, err := newConnector(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(c), nil
}

func (engine) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }
func (engine) Placeholder(int) string        { return "?" }
func (engine) DefaultSchema(cfg driver.Config) string {
	return cfg.Database
}

// Paginate: CQL has no OFFSET, so fetch limit+offset rows and let the core
// drop the first offset ones. Deep pages therefore read every earlier row.
func (engine) Paginate(q string, limit, offset int) (string, int) {
	return fmt.Sprintf("%s LIMIT %d", q, limit+offset), offset
}

// The browser's total uses SELECT COUNT(*) FROM t, which CQL supports but
// executes as a full scan across the cluster: it can be slow (and time out) on
// large tables.

func (engine) Version(ctx context.Context, db *sql.DB) string {
	v := driver.QueryString(ctx, db, "SELECT release_version FROM system.local")
	if v == "" {
		return "Cassandra"
	}
	return "Cassandra " + v
}

func (engine) CurrentDatabase(context.Context, *sql.DB) string { return "" }

// isSystemKeyspace hides Cassandra's (and DSE/Scylla's) internal keyspaces.
func isSystemKeyspace(ks string) bool {
	return strings.HasPrefix(ks, "system") || strings.HasPrefix(ks, "dse_") || ks == "solr_admin" || ks == "OpsCenter"
}

func (engine) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT keyspace_name FROM system_schema.keyspaces")
	if err != nil {
		return nil, err
	}
	all, err := driver.ScanStrings(rows)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, ks := range all {
		if !isSystemKeyspace(ks) {
			out = append(out, ks)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Databases lists the keyspaces too; Schemas is what the browser uses.
func (e engine) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	return e.Schemas(ctx, db)
}

func (engine) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	out := []driver.Table{}
	read := func(q, typ string) error {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ks, name string
			if err := rows.Scan(&ks, &name); err != nil {
				return err
			}
			if !isSystemKeyspace(ks) {
				out = append(out, driver.Table{Schema: ks, Name: name, Type: typ})
			}
		}
		return rows.Err()
	}
	if err := read("SELECT keyspace_name, table_name FROM system_schema.tables", "table"); err != nil {
		return nil, err
	}
	// Materialized views; older servers or ones with views disabled may lack them.
	_ = read("SELECT keyspace_name, view_name FROM system_schema.views", "view")
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

type cqlColumn struct {
	driver.Column
	kind string
	pos  int
}

func kindRank(kind string) int {
	switch kind {
	case "partition_key":
		return 0
	case "clustering":
		return 1
	case "static":
		return 2
	}
	return 3
}

func (engine) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT column_name, type, kind, position FROM system_schema.columns WHERE keyspace_name = ? AND table_name = ?",
		schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []cqlColumn
	for rows.Next() {
		var c cqlColumn
		var pos sql.NullInt64
		if err := rows.Scan(&c.Name, &c.Type, &c.kind, &pos); err != nil {
			return nil, err
		}
		c.pos = int(pos.Int64)
		c.PrimaryKey = c.kind == "partition_key" || c.kind == "clustering"
		c.Nullable = !c.PrimaryKey
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sortColumns(cols), nil
}

// sortColumns orders columns like CQL's DESCRIBE: partition key, clustering
// columns (by position), then static and regular columns by name.
func sortColumns(cols []cqlColumn) []driver.Column {
	sort.SliceStable(cols, func(i, j int) bool {
		a, b := cols[i], cols[j]
		if ra, rb := kindRank(a.kind), kindRank(b.kind); ra != rb {
			return ra < rb
		}
		if a.kind == "partition_key" || a.kind == "clustering" {
			return a.pos < b.pos
		}
		return a.Name < b.Name
	})
	out := make([]driver.Column, len(cols))
	for i, c := range cols {
		out[i] = c.Column
	}
	return out
}
