package driver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Catalog helpers shared by the SQLite-family engines (SQLite, libSQL,
// Cloudflare D1). They only use sqlite_master and the pragma table-valued
// functions, so they work over any transport that runs plain SQLite SQL.

// SQLiteTableFilter returns a condition on column col that hides tables whose
// names start with one of hiddenPrefixes (matched literally, not as LIKE
// patterns), e.g. "sqlite_" for SQLite's own bookkeeping tables.
func SQLiteTableFilter(col string, hiddenPrefixes []string) string {
	if len(hiddenPrefixes) == 0 {
		return "1 = 1"
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`, `'`, `''`)
	parts := make([]string, len(hiddenPrefixes))
	for i, p := range hiddenPrefixes {
		parts[i] = col + ` NOT LIKE '` + esc.Replace(p) + `%' ESCAPE '\'`
	}
	return strings.Join(parts, " AND ")
}

// SQLiteTables lists the tables and views of the main schema.
func SQLiteTables(ctx context.Context, db *sql.DB, hiddenPrefixes []string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `SELECT 'main', name, type FROM sqlite_master
		WHERE type IN ('table', 'view') AND `+SQLiteTableFilter("name", hiddenPrefixes)+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return ScanTables(rows)
}

// SQLiteColumns describes a table through pragma_table_info.
func SQLiteColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	if schema == "" {
		schema = "main"
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?, ?) ORDER BY cid`, table, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var (
			c       Column
			typ     sql.NullString
			notNull int64
			def     sql.NullString
			pk      int64
		)
		if err := rows.Scan(&c.Name, &typ, &notNull, &def, &pk); err != nil {
			return nil, err
		}
		c.Type = typ.String
		c.Nullable = notNull == 0
		c.PrimaryKey = pk > 0
		if def.Valid {
			c.Default = &def.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SQLiteForeignKeys returns the single-column references leaving and
// entering table. SQLite has no catalog view of incoming references, so one
// query joins sqlite_master with pragma_foreign_key_list for every table
// (one round trip, which matters for the HTTP engines) and keeps only the
// constraints touching table. Composite keys can't be followed from one cell
// and are left out.
func SQLiteForeignKeys(ctx context.Context, db *sql.DB, schema, table string, hiddenPrefixes []string) (out, in []ForeignKey, err error) {
	if schema == "" {
		schema = "main"
	}
	rows, err := db.QueryContext(ctx, `SELECT m.name, p.id, MAX(p."from"), MAX(p."table"), MAX(COALESCE(p."to", ''))
		FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) p
		WHERE m.type = 'table' AND `+SQLiteTableFilter("m.name", hiddenPrefixes)+`
		  AND (m.name = ? COLLATE NOCASE OR p."table" = ? COLLATE NOCASE)
		GROUP BY m.name, p.id
		HAVING COUNT(*) = 1
		ORDER BY m.name, p.id`, table, table)
	if err != nil {
		return nil, nil, err
	}
	type fkRow struct {
		table, from, ref, to string
		id                   int64
	}
	var all []fkRow
	for rows.Next() {
		var r fkRow
		if err := rows.Scan(&r.table, &r.id, &r.from, &r.ref, &r.to); err != nil {
			rows.Close()
			return nil, nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// REFERENCES t without a column means t's primary key (or its rowid).
	pks := map[string]string{}
	pkOf := func(t string) string {
		if pk, ok := pks[t]; ok {
			return pk
		}
		pk := "rowid"
		if cols, err := SQLiteColumns(ctx, db, schema, t); err == nil {
			for _, c := range cols {
				if c.PrimaryKey {
					pk = c.Name
					break
				}
			}
		}
		pks[t] = pk
		return pk
	}

	out, in = []ForeignKey{}, []ForeignKey{}
	for _, r := range all {
		to := r.to
		if to == "" {
			to = pkOf(r.ref)
		}
		f := ForeignKey{Name: fmt.Sprintf("fk_%s_%d", r.table, r.id), Schema: schema, Table: r.table,
			Column: r.from, RefSchema: schema, RefTable: r.ref, RefColumn: to}
		if strings.EqualFold(r.table, table) {
			out = append(out, f)
		}
		if strings.EqualFold(r.ref, table) {
			in = append(in, f)
		}
	}
	return out, in, nil
}
