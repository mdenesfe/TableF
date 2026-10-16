package driver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Index describes one index of a table.
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	// Method is the access method or kind (btree, hash, gin, CLUSTERED…), if known.
	Method string `json:"method"`
	// Definition is the CREATE INDEX statement or the engine's description, if known.
	Definition string `json:"definition"`
	// Size in bytes and Scans since the statistics were reset; -1 when unknown.
	Size  int64 `json:"size"`
	Scans int64 `json:"scans"`
}

// Indexer is implemented by engines that can list a table's indexes.
type Indexer interface {
	Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error)
}

// ColumnSep separates the columns of an index in the list a query returns
// (ASCII unit separator: expressions such as COALESCE(a, b) contain commas).
const ColumnSep = "\x1f"

// ScanIndexes reads rows of (name, columns, unique, primary, method,
// definition, size, scans) where columns is a ColumnSep-separated list (or,
// without one, comma-separated) and size and scans may be NULL.
func ScanIndexes(rows *sql.Rows) ([]Index, error) {
	defer rows.Close()
	out := []Index{}
	for rows.Next() {
		var (
			ix          Index
			cols        sql.NullString
			method, def sql.NullString
			size, scans sql.NullInt64
		)
		if err := rows.Scan(&ix.Name, &cols, &ix.Unique, &ix.Primary, &method, &def, &size, &scans); err != nil {
			return nil, err
		}
		ix.Columns = splitList(cols.String)
		ix.Method, ix.Definition = method.String, def.String
		ix.Size, ix.Scans = -1, -1
		if size.Valid {
			ix.Size = size.Int64
		}
		if scans.Valid {
			ix.Scans = scans.Int64
		}
		out = append(out, ix)
	}
	return out, rows.Err()
}

func splitList(s string) []string {
	sep := ColumnSep
	if !strings.Contains(s, sep) {
		sep = ","
	}
	out := []string{}
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Indexes of PostgreSQL, with size and scan counts.
func (postgres) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ic.relname,
			(SELECT string_agg(COALESCE(a.attname, pg_get_indexdef(i.indexrelid, k.n + 1, true)), chr(31) ORDER BY k.n)
				FROM generate_subscripts(i.indkey, 1) AS k(n)
				LEFT JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[k.n] AND i.indkey[k.n] <> 0),
			i.indisunique, i.indisprimary, am.amname, pg_get_indexdef(i.indexrelid),
			pg_relation_size(i.indexrelid), s.idx_scan
		FROM pg_index i
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_class tc ON tc.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = tc.relnamespace
		JOIN pg_am am ON am.oid = ic.relam
		LEFT JOIN pg_stat_all_indexes s ON s.indexrelid = i.indexrelid
		WHERE n.nspname = $1 AND tc.relname = $2
		ORDER BY i.indisprimary DESC, ic.relname`, schema, table)
	if err != nil {
		return nil, err
	}
	return ScanIndexes(rows)
}

// Indexes of MySQL / MariaDB from information_schema.STATISTICS.
func (mysqlDriver) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT INDEX_NAME,
			GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX SEPARATOR ','),
			MIN(NON_UNIQUE) = 0, INDEX_NAME = 'PRIMARY', MIN(INDEX_TYPE), NULLIF(MIN(INDEX_COMMENT), ''),
			NULL, NULL
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = COALESCE(NULLIF(?, ''), DATABASE()) AND TABLE_NAME = ?
		GROUP BY INDEX_NAME
		ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME`, schema, table)
	if err != nil {
		return nil, err
	}
	return ScanIndexes(rows)
}

// Indexes of SQLite from pragma_index_list / pragma_index_info.
func (sqlite) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	if schema == "" {
		schema = "main"
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT l.name,
			(SELECT group_concat(COALESCE(i.name, '(expression)'), char(31)) FROM pragma_index_info(l.name, %[1]s) i),
			l."unique", l.origin = 'pk',
			CASE l.origin WHEN 'pk' THEN 'primary key' WHEN 'u' THEN 'unique constraint' ELSE 'index' END,
			(SELECT m.sql FROM %[2]s.sqlite_master m WHERE m.type = 'index' AND m.name = l.name),
			NULL, NULL
		FROM pragma_index_list(?, %[1]s) l
		ORDER BY l.origin = 'pk' DESC, l.name`, sqliteString(schema), QuoteWith(schema, `"`)), table)
	if err != nil {
		return nil, err
	}
	list, err := ScanIndexes(rows)
	if err != nil {
		return nil, err
	}
	// An INTEGER PRIMARY KEY is the rowid itself: the table's own b-tree is
	// keyed by it, so pragma_index_list has no entry for it.
	var pk string
	err = db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT (SELECT name FROM pragma_table_info(?, %[1]s) WHERE pk = 1)
		WHERE (SELECT count(*) FROM pragma_table_info(?, %[1]s) WHERE pk > 0) = 1
			AND (SELECT upper(type) FROM pragma_table_info(?, %[1]s) WHERE pk = 1) = 'INTEGER'
			AND NOT EXISTS (SELECT 1 FROM pragma_index_list(?, %[1]s) WHERE origin = 'pk')`, sqliteString(schema)),
		table, table, table, table).Scan(&pk)
	if err == nil && pk != "" {
		list = append([]Index{{Name: "(rowid)", Columns: []string{pk}, Unique: true, Primary: true, Method: "rowid",
			Definition: "INTEGER PRIMARY KEY: the table is stored ordered by it", Size: -1, Scans: -1}}, list...)
	}
	return list, nil
}

func sqliteString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
