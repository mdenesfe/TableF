package oracle

import (
	"context"
	"database/sql"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Principals lists users not shipped with Oracle and the roles granted to this session.
func (oracle) Principals(ctx context.Context, db *sql.DB) ([]driver.Principal, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT username, 'user' FROM all_users WHERE oracle_maintained = 'N'
		UNION ALL
		SELECT role, 'role' FROM session_roles
		ORDER BY 2 DESC, 1`)
	if err != nil {
		return nil, err
	}
	return driver.ScanPrincipals(rows)
}
