package clickhouse

import (
	"context"
	"database/sql"

	"github.com/mdenesfe/TableF/internal/driver"
)

// TableInfo reads system.tables and the active parts of system.parts.
func (clickhouse) TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]driver.InfoField, error) {
	return driver.ScanInfo(ctx, db, `
		SELECT
			t.total_bytes AS total_size,
			p.compressed AS compressed_size,
			p.uncompressed AS uncompressed_size,
			t.total_rows AS estimated_rows,
			p.parts AS part_count,
			t.engine AS engine,
			nullIf(t.partition_key, '') AS partition_key,
			nullIf(t.sorting_key, '') AS sorting_key,
			nullIf(t.primary_key, '') AS primary_key,
			toString(t.metadata_modification_time) AS modified,
			nullIf(t.comment, '') AS comment
		FROM system.tables t
		LEFT JOIN (
			SELECT database, table, count() AS parts,
				sum(data_compressed_bytes) AS compressed, sum(data_uncompressed_bytes) AS uncompressed
			FROM system.parts WHERE active GROUP BY database, table
		) p ON p.database = t.database AND p.table = t.name
		WHERE t.database = ? AND t.name = ?`, schema, table)
}

// Indexes: the primary (sorting) key plus data skipping indices.
func (clickhouse) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT 'PRIMARY', primary_key, false, true, 'primary key', concat('ORDER BY ', sorting_key), NULL, NULL
		FROM system.tables WHERE database = ? AND name = ? AND primary_key != ''
		UNION ALL
		SELECT name, expr, false, false, type, concat(type_full, ' GRANULARITY ', toString(granularity)),
			toInt64(data_compressed_bytes), NULL
		FROM system.data_skipping_indices WHERE database = ? AND table = ?`, schema, table, schema, table)
	if err != nil {
		return nil, err
	}
	return driver.ScanIndexes(rows)
}
