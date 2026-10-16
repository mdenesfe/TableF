package driver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type postgres struct{}

func init() { Register(postgres{}) }

func (postgres) Name() string { return "postgres" }

func (postgres) Open(cfg Config) (*sql.DB, error) {
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	dbname := cfg.Database
	if dbname == "" {
		dbname = "postgres"
	}
	ssl := cfg.SSLMode
	if ssl == "" {
		ssl = "prefer"
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, port),
		Path:     "/" + dbname,
		RawQuery: url.Values{"sslmode": {ssl}, "application_name": {"TableF"}}.Encode(),
	}
	return sql.Open("pgx", u.String())
}

func (postgres) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY datname`)
	if err != nil {
		return nil, err
	}
	return ScanStrings(rows)
}

func (postgres) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT nspname FROM pg_namespace
		WHERE nspname NOT IN ('pg_catalog', 'information_schema')
		  AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp%'
		ORDER BY nspname`)
	if err != nil {
		return nil, err
	}
	return ScanStrings(rows)
}

func (postgres) Tables(ctx context.Context, db *sql.DB) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_schema, table_name, table_type
		FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND table_schema NOT LIKE 'pg_toast%'
		ORDER BY table_schema, table_name`)
	if err != nil {
		return nil, err
	}
	return ScanTables(rows)
}

// Columns reads the column list from information_schema (for its portable
// type names) and the primary key from pg_index, which is much cheaper than
// information_schema's constraint views on databases with many tables.
func (postgres) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.column_name,
		       c.data_type,
		       c.is_nullable = 'YES',
		       c.column_default,
		       pk.attname IS NOT NULL
		FROM information_schema.columns c
		LEFT JOIN (
		  SELECT a.attname
		  FROM pg_index i
		  JOIN pg_class t ON t.oid = i.indrelid
		  JOIN pg_namespace n ON n.oid = t.relnamespace
		  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey)
		  WHERE i.indisprimary AND n.nspname = $1 AND t.relname = $2
		) pk ON pk.attname = c.column_name
		WHERE c.table_schema = $1 AND c.table_name = $2
		ORDER BY c.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	return scanColumns(rows)
}

// portableColumns finds primary keys through information_schema only, for
// PostgreSQL-compatible engines whose pg_index is incomplete or missing.
func portableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.column_name,
		       c.data_type,
		       c.is_nullable = 'YES',
		       c.column_default,
		       EXISTS (
		         SELECT 1
		         FROM information_schema.table_constraints tc
		         JOIN information_schema.key_column_usage kcu
		           ON tc.constraint_name = kcu.constraint_name
		          AND tc.table_schema = kcu.table_schema
		         WHERE tc.constraint_type = 'PRIMARY KEY'
		           AND tc.table_schema = c.table_schema
		           AND tc.table_name = c.table_name
		           AND kcu.column_name = c.column_name
		       )
		FROM information_schema.columns c
		WHERE c.table_schema = $1 AND c.table_name = $2
		ORDER BY c.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	return scanColumns(rows)
}

// Only single-column keys are listed; composite keys are rare in practice and
// can't be followed from one cell.
const pgForeignKeys = `
	SELECT c.conname, cn.nspname, cl.relname, a.attname, rn.nspname, rl.relname, ra.attname
	FROM pg_constraint c
	JOIN pg_class cl ON cl.oid = c.conrelid
	JOIN pg_namespace cn ON cn.oid = cl.relnamespace
	JOIN pg_class rl ON rl.oid = c.confrelid
	JOIN pg_namespace rn ON rn.oid = rl.relnamespace
	JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
	JOIN pg_attribute ra ON ra.attrelid = c.confrelid AND ra.attnum = c.confkey[1]
	WHERE c.contype = 'f' AND array_length(c.conkey, 1) = 1 AND %s
	ORDER BY 2, 3, 4`

func (postgres) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, []ForeignKey, error) {
	query := func(cond string) ([]ForeignKey, error) {
		rows, err := db.QueryContext(ctx, fmt.Sprintf(pgForeignKeys, cond), schema, table)
		if err != nil {
			return nil, err
		}
		return ScanForeignKeys(rows)
	}
	out, err := query(`cn.nspname = $1 AND cl.relname = $2`)
	if err != nil {
		return nil, nil, err
	}
	in, err := query(`rn.nspname = $1 AND rl.relname = $2`)
	return out, in, err
}

func (postgres) Routines(ctx context.Context, db *sql.DB) ([]Routine, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT p.oid::text, n.nspname, p.proname,
		       CASE p.prokind WHEN 'p' THEN 'procedure' ELSE 'function' END,
		       pg_get_function_identity_arguments(p.oid),
		       COALESCE(pg_get_function_result(p.oid), '')
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		LEFT JOIN pg_depend d ON d.objid = p.oid AND d.deptype = 'e'
		WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND n.nspname NOT LIKE 'pg_toast%'
		  AND p.prokind IN ('f', 'p')
		  AND d.objid IS NULL -- skip functions owned by extensions
		ORDER BY n.nspname, p.proname, 5`)
	if err != nil {
		return nil, err
	}
	return scanRoutines(rows)
}

func (postgres) RoutineDefinition(ctx context.Context, db *sql.DB, r Routine) (string, error) {
	var def string
	err := db.QueryRowContext(ctx, `SELECT pg_get_functiondef($1::oid)`, r.ID).Scan(&def)
	return def, err
}

func (postgres) QuoteIdent(name string) string { return QuoteWith(name, `"`) }

func (postgres) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

func (postgres) DefaultSchema(Config) string { return "public" }

func scanRoutines(rows *sql.Rows) ([]Routine, error) {
	defer rows.Close()
	out := []Routine{}
	for rows.Next() {
		var r Routine
		if err := rows.Scan(&r.ID, &r.Schema, &r.Name, &r.Kind, &r.Arguments, &r.ReturnType); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanColumns(rows *sql.Rows) ([]Column, error) {
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var c Column
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &def, &c.PrimaryKey); err != nil {
			return nil, err
		}
		if def.Valid {
			c.Default = &def.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
