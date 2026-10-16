package driver

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
)

type mysqlDriver struct{}

func init() { Register(mysqlDriver{}) }

func (mysqlDriver) Name() string { return "mysql" }

// mysqlConfig turns a Config into the go-sql-driver configuration.
func mysqlConfig(cfg Config) *mysql.Config {
	port := cfg.Port
	if port == 0 {
		port = 3306
	}
	mc := mysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	mc.Addr = fmt.Sprintf("%s:%d", cfg.Host, port)
	mc.DBName = cfg.Database
	mc.ParseTime = true
	mc.Loc = time.UTC
	mc.MultiStatements = false
	// Same meaning as libpq's sslmode: "require" encrypts without checking
	// the certificate, the verify modes check it against the system roots.
	switch cfg.SSLMode {
	case "require":
		mc.TLSConfig = "skip-verify"
	case "verify-ca", "verify-full":
		mc.TLSConfig = "true"
	case "prefer":
		mc.TLSConfig = "preferred"
	}
	return mc
}

func (mysqlDriver) Open(cfg Config) (*sql.DB, error) {
	return sql.Open("mysql", mysqlConfig(cfg).FormatDSN())
}

func (mysqlDriver) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT schema_name FROM information_schema.schemata ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	return ScanStrings(rows)
}

// In MySQL a schema is the database itself.
func (mysqlDriver) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DATABASE() WHERE DATABASE() IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	return ScanStrings(rows)
}

func (mysqlDriver) Tables(ctx context.Context, db *sql.DB) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_schema, table_name, table_type
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	return ScanTables(rows)
}

func (mysqlDriver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, column_type, is_nullable = 'YES', column_default, column_key = 'PRI'
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	return scanColumns(rows)
}

// ForeignKeys lists single-column keys only: grouping by constraint and
// keeping groups of one row drops composite keys in the same pass.
func (mysqlDriver) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, []ForeignKey, error) {
	query := func(cond string) ([]ForeignKey, error) {
		rows, err := db.QueryContext(ctx, `
			SELECT constraint_name, MAX(table_schema), table_name, MAX(column_name),
			       MAX(referenced_table_schema), MAX(referenced_table_name), MAX(referenced_column_name)
			FROM information_schema.key_column_usage
			WHERE referenced_table_name IS NOT NULL AND `+cond+`
			GROUP BY constraint_schema, constraint_name, table_name
			HAVING COUNT(*) = 1
			ORDER BY 2, 3, 4`, schema, table)
		if err != nil {
			return nil, err
		}
		return ScanForeignKeys(rows)
	}
	out, err := query(`table_schema = ? AND table_name = ?`)
	if err != nil {
		return nil, nil, err
	}
	in, err := query(`referenced_table_schema = ? AND referenced_table_name = ?`)
	return out, in, err
}

func (mysqlDriver) Routines(ctx context.Context, db *sql.DB) ([]Routine, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.routine_name, r.routine_schema, r.routine_name, LOWER(r.routine_type),
		       COALESCE((
		         SELECT GROUP_CONCAT(CONCAT_WS(' ', p.parameter_mode, p.parameter_name, p.dtd_identifier)
		                ORDER BY p.ordinal_position SEPARATOR ', ')
		         FROM information_schema.parameters p
		         WHERE p.specific_schema = r.routine_schema AND p.specific_name = r.specific_name
		           AND p.ordinal_position > 0
		       ), ''),
		       COALESCE(r.dtd_identifier, '')
		FROM information_schema.routines r
		WHERE r.routine_schema = DATABASE()
		ORDER BY r.routine_name`)
	if err != nil {
		return nil, err
	}
	return scanRoutines(rows)
}

func (m mysqlDriver) RoutineDefinition(ctx context.Context, db *sql.DB, r Routine) (string, error) {
	kind := "FUNCTION"
	if r.Kind == "procedure" {
		kind = "PROCEDURE"
	}
	rows, err := db.QueryContext(ctx, "SHOW CREATE "+kind+" "+QualifiedName(m, r.Schema, r.Name))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	if !rows.Next() {
		return "", fmt.Errorf("%s %s not found", kind, r.Name)
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	// Column 3 is "Create Function" / "Create Procedure".
	if len(vals) > 2 && vals[2].Valid {
		return vals[2].String, nil
	}
	return "", fmt.Errorf("no definition available (missing privileges?)")
}

func (mysqlDriver) QuoteIdent(name string) string { return QuoteWith(name, "`") }

func (mysqlDriver) Placeholder(int) string { return "?" }

func (mysqlDriver) DefaultSchema(cfg Config) string { return cfg.Database }
