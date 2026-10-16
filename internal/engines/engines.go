// Package engines links every database engine into the binary. Each engine
// package registers itself with the driver package from init. Engines that
// need cgo (DuckDB) are linked from a separate, build-tagged file.
package engines

import (
	_ "github.com/mdenesfe/TableF/internal/engines/bigquery"
	_ "github.com/mdenesfe/TableF/internal/engines/cassandra"
	_ "github.com/mdenesfe/TableF/internal/engines/clickhouse"
	_ "github.com/mdenesfe/TableF/internal/engines/d1"
	_ "github.com/mdenesfe/TableF/internal/engines/dynamodb"
	_ "github.com/mdenesfe/TableF/internal/engines/elasticsearch"
	_ "github.com/mdenesfe/TableF/internal/engines/libsql"
	_ "github.com/mdenesfe/TableF/internal/engines/mongodb"
	_ "github.com/mdenesfe/TableF/internal/engines/mssql"
	_ "github.com/mdenesfe/TableF/internal/engines/oracle"
	_ "github.com/mdenesfe/TableF/internal/engines/redis"
	_ "github.com/mdenesfe/TableF/internal/engines/snowflake"
	_ "github.com/mdenesfe/TableF/internal/engines/vertica"
)
