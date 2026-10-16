package oracle

import (
	"context"
	"database/sql"

	"github.com/mdenesfe/TableF/internal/driver"
)

// TableInfo reads optimizer statistics from ALL_TABLES, segment sizes from
// USER_SEGMENTS (own tables only; others show no size) and timestamps from
// ALL_OBJECTS.
func (oracle) TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]driver.InfoField, error) {
	return driver.ScanInfo(ctx, db, `
		SELECT
			(SELECT SUM(s.bytes) FROM user_segments s WHERE o.owner = USER AND (s.segment_name = o.object_name
				OR s.segment_name IN (SELECT i.index_name FROM all_indexes i WHERE i.table_owner = o.owner AND i.table_name = o.object_name))) AS total_size,
			(SELECT SUM(s.bytes) FROM user_segments s WHERE o.owner = USER AND s.segment_name = o.object_name) AS data_size,
			t.num_rows AS estimated_rows,
			t.avg_row_len AS avg_row_size,
			t.blocks AS blocks,
			(SELECT COUNT(*) FROM all_indexes i WHERE i.table_owner = o.owner AND i.table_name = o.object_name) AS index_count,
			(SELECT COUNT(*) FROM all_triggers g WHERE g.table_owner = o.owner AND g.table_name = o.object_name) AS trigger_count,
			t.tablespace_name AS tablespace,
			t.partitioned AS partitioned,
			t.temporary AS temporary,
			TO_CHAR(t.last_analyzed, 'YYYY-MM-DD HH24:MI:SS') AS last_analyzed,
			TO_CHAR(o.created, 'YYYY-MM-DD HH24:MI:SS') AS created,
			TO_CHAR(o.last_ddl_time, 'YYYY-MM-DD HH24:MI:SS') AS last_ddl,
			o.status AS status,
			LOWER(o.object_type) AS kind,
			c.comments AS "comment"
		FROM all_objects o
		LEFT JOIN all_tables t ON t.owner = o.owner AND t.table_name = o.object_name
		LEFT JOIN all_tab_comments c ON c.owner = o.owner AND c.table_name = o.object_name
		WHERE o.owner = :1 AND o.object_name = :2 AND o.object_type IN ('TABLE', 'VIEW')`, schema, table)
}

// Indexes lists ALL_INDEXES with their columns; primary keys come from the
// constraint that uses the index.
func (oracle) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT i.index_name,
			(SELECT LISTAGG(c.column_name, CHR(31)) WITHIN GROUP (ORDER BY c.column_position)
				FROM all_ind_columns c WHERE c.index_owner = i.owner AND c.index_name = i.index_name),
			CASE WHEN i.uniqueness = 'UNIQUE' THEN 1 ELSE 0 END,
			CASE WHEN EXISTS (SELECT 1 FROM all_constraints k WHERE k.owner = i.table_owner AND k.table_name = i.table_name
				AND k.constraint_type = 'P' AND k.index_name = i.index_name) THEN 1 ELSE 0 END,
			LOWER(i.index_type), NULL,
			(SELECT SUM(s.bytes) FROM user_segments s WHERE i.owner = USER AND s.segment_name = i.index_name),
			NULL
		FROM all_indexes i
		WHERE i.table_owner = :1 AND i.table_name = :2
		ORDER BY 4 DESC, i.index_name`, schema, table)
	if err != nil {
		return nil, err
	}
	return driver.ScanIndexes(rows)
}
