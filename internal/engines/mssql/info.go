package mssql

import (
	"context"
	"database/sql"

	"github.com/mdenesfe/TableF/internal/driver"
)

// TableInfo reports sizes and rows from sys.dm_db_partition_stats (8 KB
// pages), plus timestamps, counts and the MS_Description comment.
func (mssql) TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]driver.InfoField, error) {
	return driver.ScanInfo(ctx, db, `
		SELECT
			p.reserved * 8192 AS total_size,
			p.data * 8192 AS data_size,
			(p.used - p.data) * 8192 AS index_size,
			(p.reserved - p.used) * 8192 AS unused_size,
			p.row_count AS estimated_rows,
			(SELECT COUNT(*) FROM sys.indexes i WHERE i.object_id = o.object_id AND i.index_id > 0) AS index_count,
			(SELECT COUNT(*) FROM sys.triggers g WHERE g.parent_id = o.object_id) AS trigger_count,
			(SELECT COUNT(*) FROM sys.partitions x WHERE x.object_id = o.object_id AND x.index_id IN (0, 1)) AS partition_count,
			CASE WHEN EXISTS (SELECT 1 FROM sys.indexes i WHERE i.object_id = o.object_id AND i.index_id = 1) THEN 'clustered' ELSE 'heap' END AS storage,
			CONVERT(varchar(19), o.create_date, 120) AS created,
			CONVERT(varchar(19), o.modify_date, 120) AS modified,
			o.type_desc AS kind,
			o.object_id AS object_id,
			CAST(ep.value AS nvarchar(max)) AS comment
		FROM sys.objects o
		JOIN sys.schemas s ON s.schema_id = o.schema_id
		OUTER APPLY (
			SELECT SUM(ps.reserved_page_count) AS reserved, SUM(ps.used_page_count) AS used,
				SUM(CASE WHEN ps.index_id IN (0, 1) THEN ps.in_row_data_page_count + ps.lob_used_page_count + ps.row_overflow_used_page_count ELSE 0 END) AS data,
				SUM(CASE WHEN ps.index_id IN (0, 1) THEN ps.row_count ELSE 0 END) AS row_count
			FROM sys.dm_db_partition_stats ps WHERE ps.object_id = o.object_id
		) p
		LEFT JOIN sys.extended_properties ep ON ep.major_id = o.object_id AND ep.minor_id = 0 AND ep.class = 1 AND ep.name = 'MS_Description'
		WHERE s.name = @p1 AND o.name = @p2 AND o.type IN ('U', 'V')`, schema, table)
}

// Indexes lists sys.indexes with their key columns, size and read count.
func (mssql) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT i.name,
			STUFF((SELECT ',' + c.name FROM sys.index_columns ic
				JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
				WHERE ic.object_id = i.object_id AND ic.index_id = i.index_id AND ic.is_included_column = 0
				ORDER BY ic.key_ordinal FOR XML PATH('')), 1, 1, ''),
			i.is_unique, i.is_primary_key, i.type_desc,
			NULLIF(i.filter_definition, ''),
			(SELECT SUM(ps.used_page_count) * 8192 FROM sys.dm_db_partition_stats ps WHERE ps.object_id = i.object_id AND ps.index_id = i.index_id),
			(SELECT SUM(us.user_seeks + us.user_scans + us.user_lookups) FROM sys.dm_db_index_usage_stats us
				WHERE us.database_id = DB_ID() AND us.object_id = i.object_id AND us.index_id = i.index_id)
		FROM sys.indexes i
		JOIN sys.objects o ON o.object_id = i.object_id
		JOIN sys.schemas s ON s.schema_id = o.schema_id
		WHERE s.name = @p1 AND o.name = @p2 AND i.index_id > 0
		ORDER BY i.is_primary_key DESC, i.name`, schema, table)
	if err != nil {
		return nil, err
	}
	return driver.ScanIndexes(rows)
}
