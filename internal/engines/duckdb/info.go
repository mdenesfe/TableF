package duckdb

import (
	"context"
	"database/sql"

	"github.com/mdenesfe/TableF/internal/driver"
)

// TableInfo reads duckdb_tables(); views only report what duckdb_views() knows.
func (duckDriver) TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]driver.InfoField, error) {
	return driver.ScanInfo(ctx, db, `
		SELECT
			t.estimated_size AS estimated_rows,
			t.column_count AS column_count,
			t.index_count AS index_count,
			t.check_constraint_count AS check_count,
			CASE WHEN t.has_primary_key THEN 'yes' ELSE 'no' END AS primary_key,
			CASE WHEN t.temporary THEN 'yes' ELSE 'no' END AS temporary,
			t.database_name AS catalog,
			t.comment AS comment
		FROM duckdb_tables() t
		WHERE t.schema_name = ? AND t.table_name = ?`, schema, table)
}

// Indexes from duckdb_indexes(); primary keys and unique constraints are
// implicit there, so they come from duckdb_constraints().
func (duckDriver) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT constraint_type || ' (' || array_to_string(constraint_column_names, ', ') || ')',
			array_to_string(constraint_column_names, chr(31)), true, constraint_type = 'PRIMARY KEY',
			lower(constraint_type), constraint_text, NULL::BIGINT, NULL::BIGINT
		FROM duckdb_constraints()
		WHERE schema_name = ? AND table_name = ? AND constraint_type IN ('PRIMARY KEY', 'UNIQUE')
		UNION ALL
		SELECT index_name, trim(expressions, '[]'), is_unique, is_primary, 'art', sql, NULL::BIGINT, NULL::BIGINT
		FROM duckdb_indexes()
		WHERE schema_name = ? AND table_name = ?`, schema, table, schema, table)
	if err != nil {
		return nil, err
	}
	return driver.ScanIndexes(rows)
}
