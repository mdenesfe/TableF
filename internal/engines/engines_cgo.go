//go:build cgo

package engines

// DuckDB embeds its C++ library, so it is only available in cgo builds.
import _ "github.com/mdenesfe/TableF/internal/engines/duckdb"

const hasDuckDB = true
