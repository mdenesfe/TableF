package engines

import (
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
)

// The ids the connection form offers (frontend/src/lib/engines.ts).
var offered = []string{
	"postgres", "redshift", "mysql", "mariadb", "singlestore", "mssql", "cassandra", "clickhouse",
	"bigquery", "dynamodb", "elasticsearch", "libsql", "d1", "mongodb", "snowflake", "redis",
	"sqlite", "duckdb", "oracle", "cockroach", "greenplum", "vertica",
}

func TestEveryOfferedEngineIsRegistered(t *testing.T) {
	for _, id := range offered {
		if id == "duckdb" && !hasDuckDB {
			continue // built without cgo
		}
		if !driver.Known(id) {
			t.Errorf("engine %q is offered in the UI but not registered", id)
		}
	}
}
