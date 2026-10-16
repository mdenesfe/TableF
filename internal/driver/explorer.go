package driver

import (
	"context"
	"database/sql"
)

// Principal is a user or role of a database, as shown under Security.
type Principal struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // user | role
}

// Securer is implemented by engines that can list their users and roles.
type Securer interface {
	Principals(ctx context.Context, db *sql.DB) ([]Principal, error)
}

// ScanPrincipals reads rows of (name, kind).
func ScanPrincipals(rows *sql.Rows) ([]Principal, error) {
	defer rows.Close()
	out := []Principal{}
	for rows.Next() {
		var p Principal
		if err := rows.Scan(&p.Name, &p.Kind); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Principals of PostgreSQL: roles that can log in are users, the rest roles.
// Built-in pg_* roles are left out.
func (postgres) Principals(ctx context.Context, db *sql.DB) ([]Principal, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT rolname, CASE WHEN rolcanlogin THEN 'user' ELSE 'role' END
		FROM pg_roles WHERE rolname NOT LIKE 'pg\_%' ORDER BY rolname`)
	if err != nil {
		return nil, err
	}
	return ScanPrincipals(rows)
}

// Principals of MySQL: accounts as user@host (needs read access to mysql.user).
func (mysqlDriver) Principals(ctx context.Context, db *sql.DB) ([]Principal, error) {
	rows, err := db.QueryContext(ctx, "SELECT CONCAT(`User`, '@', `Host`), 'user' FROM mysql.user ORDER BY 1")
	if err != nil {
		return nil, err
	}
	return ScanPrincipals(rows)
}
