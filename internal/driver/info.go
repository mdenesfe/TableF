package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// InfoField is one fact about a table, as shown in the table details pane.
// Keys are snake_case; the UI formats keys ending in _size as bytes and
// counts (rows, *_count, *_scans) as numbers. A nil Value is NULL.
type InfoField struct {
	Key   string  `json:"key"`
	Value *string `json:"value"`
}

// TableInformer is implemented by engines that can describe a table's
// storage and metadata (sizes, row estimate, comment, timestamps…).
type TableInformer interface {
	TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]InfoField, error)
}

// ErrNoTable is returned by ScanInfo when the query matched no row.
var ErrNoTable = errors.New("table not found")

// ScanInfo runs a query that returns a single row and turns each column into
// a field named after it.
func ScanInfo(ctx context.Context, db *sql.DB, query string, args ...any) ([]InfoField, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, ErrNoTable
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make([]InfoField, len(cols))
	for i, c := range cols {
		out[i].Key = c
		if vals[i].Valid {
			v := vals[i].String
			out[i].Value = &v
		}
	}
	return out, rows.Err()
}

// TableInfo of PostgreSQL: sizes, planner and live statistics, maintenance times.
func (postgres) TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]InfoField, error) {
	return ScanInfo(ctx, db, `
		SELECT
			pg_total_relation_size(c.oid) AS total_size,
			pg_table_size(c.oid) AS data_size,
			pg_indexes_size(c.oid) AS index_size,
			pg_total_relation_size(c.oid) - pg_relation_size(c.oid) - pg_indexes_size(c.oid) AS toast_size,
			NULLIF(c.reltuples, -1)::bigint AS estimated_rows,
			s.n_live_tup AS live_rows,
			s.n_dead_tup AS dead_rows,
			(SELECT count(*) FROM pg_index i WHERE i.indrelid = c.oid) AS index_count,
			(SELECT count(*) FROM pg_trigger g WHERE g.tgrelid = c.oid AND NOT g.tgisinternal) AS trigger_count,
			s.seq_scan AS seq_scans,
			s.idx_scan AS index_scans,
			s.n_tup_ins AS inserted_rows,
			s.n_tup_upd AS updated_rows,
			s.n_tup_del AS deleted_rows,
			to_char(s.last_vacuum, 'YYYY-MM-DD HH24:MI:SS') AS last_vacuum,
			to_char(s.last_autovacuum, 'YYYY-MM-DD HH24:MI:SS') AS last_autovacuum,
			to_char(s.last_analyze, 'YYYY-MM-DD HH24:MI:SS') AS last_analyze,
			to_char(s.last_autoanalyze, 'YYYY-MM-DD HH24:MI:SS') AS last_autoanalyze,
			pg_get_userbyid(c.relowner) AS owner,
			COALESCE(t.spcname, 'pg_default') AS tablespace,
			CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'partitioned table' WHEN 'v' THEN 'view'
				WHEN 'm' THEN 'materialized view' WHEN 'f' THEN 'foreign table' ELSE c.relkind::text END AS kind,
			CASE c.relpersistence WHEN 'u' THEN 'unlogged' WHEN 't' THEN 'temporary' ELSE 'permanent' END AS persistence,
			c.oid::bigint AS oid,
			obj_description(c.oid, 'pg_class') AS comment
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_all_tables s ON s.relid = c.oid
		LEFT JOIN pg_tablespace t ON t.oid = c.reltablespace
		WHERE n.nspname = $1 AND c.relname = $2`, schema, table)
}

// TableInfo of MySQL / MariaDB from information_schema.TABLES.
func (mysqlDriver) TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]InfoField, error) {
	return ScanInfo(ctx, db, `
		SELECT
			t.DATA_LENGTH + t.INDEX_LENGTH AS total_size,
			t.DATA_LENGTH AS data_size,
			t.INDEX_LENGTH AS index_size,
			t.DATA_FREE AS free_size,
			t.TABLE_ROWS AS estimated_rows,
			t.AVG_ROW_LENGTH AS avg_row_size,
			(SELECT COUNT(DISTINCT s.INDEX_NAME) FROM information_schema.STATISTICS s
				WHERE s.TABLE_SCHEMA = t.TABLE_SCHEMA AND s.TABLE_NAME = t.TABLE_NAME) AS index_count,
			(SELECT COUNT(*) FROM information_schema.TRIGGERS g
				WHERE g.EVENT_OBJECT_SCHEMA = t.TABLE_SCHEMA AND g.EVENT_OBJECT_TABLE = t.TABLE_NAME) AS trigger_count,
			t.AUTO_INCREMENT AS auto_increment,
			t.ENGINE AS engine,
			t.ROW_FORMAT AS row_format,
			t.TABLE_COLLATION AS collation,
			t.CREATE_TIME AS created,
			t.UPDATE_TIME AS updated,
			t.CHECK_TIME AS checked,
			t.TABLE_TYPE AS kind,
			NULLIF(t.TABLE_COMMENT, '') AS comment
		FROM information_schema.TABLES t
		WHERE t.TABLE_SCHEMA = COALESCE(NULLIF(?, ''), DATABASE()) AND t.TABLE_NAME = ?`, schema, table)
}

// TableInfo of SQLite. Sizes come from the dbstat virtual table, which not
// every build has; without it they are left out.
func (sqlite) TableInfo(ctx context.Context, db *sql.DB, schema, table string) ([]InfoField, error) {
	if schema == "" {
		schema = "main"
	}
	master := QuoteWith(schema, `"`) + ".sqlite_master"
	fields, err := ScanInfo(ctx, db, fmt.Sprintf(`
		SELECT
			type AS kind,
			(SELECT count(*) FROM %[1]s WHERE type = 'index' AND tbl_name = m.name) AS index_count,
			(SELECT count(*) FROM %[1]s WHERE type = 'trigger' AND tbl_name = m.name) AS trigger_count,
			CASE WHEN sql LIKE '%%WITHOUT ROWID%%' THEN 'yes' ELSE 'no' END AS without_rowid,
			CASE WHEN sql LIKE '%%AUTOINCREMENT%%' THEN 'yes' ELSE 'no' END AS "autoincrement",
			rootpage AS root_page
		FROM %[1]s m WHERE m.name = ? AND m.type IN ('table', 'view')`, master), table)
	if err != nil {
		return nil, err
	}
	if size, err := ScanInfo(ctx, db, `
		SELECT SUM(pgsize) AS total_size,
			SUM(CASE WHEN name = ? THEN pgsize END) AS data_size,
			SUM(CASE WHEN name <> ? THEN pgsize END) AS index_size
		FROM dbstat WHERE schema = ? AND (name = ? OR name IN (SELECT name FROM `+master+` WHERE type = 'index' AND tbl_name = ?))`,
		table, table, schema, table, table); err == nil {
		fields = append(size, fields...)
	}
	if seq, err := ScanInfo(ctx, db, "SELECT seq AS last_autoincrement FROM "+QuoteWith(schema, `"`)+".sqlite_sequence WHERE name = ?", table); err == nil {
		fields = append(fields, seq...)
	}
	return fields, nil
}
