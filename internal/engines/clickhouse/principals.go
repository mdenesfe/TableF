package clickhouse

import (
	"context"
	"database/sql"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Principals lists ClickHouse users and roles (needs SHOW USERS / SHOW ROLES access).
func (clickhouse) Principals(ctx context.Context, db *sql.DB) ([]driver.Principal, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name, 'user' FROM system.users
		UNION ALL
		SELECT name, 'role' FROM system.roles
		ORDER BY 2 DESC, 1`)
	if err != nil {
		return nil, err
	}
	return driver.ScanPrincipals(rows)
}
