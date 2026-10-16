package mssql

import (
	"context"
	"database/sql"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Principals lists the database's users and roles (sys.database_principals of
// the database the connection is in), without the built-in system ones.
func (mssql) Principals(ctx context.Context, db *sql.DB) ([]driver.Principal, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name, CASE WHEN type = 'R' THEN 'role' ELSE 'user' END
		FROM sys.database_principals
		WHERE type IN ('S', 'U', 'G', 'E', 'X', 'C', 'K', 'R')
		  AND name NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest', 'public')
		  AND name NOT LIKE '##%'
		ORDER BY CASE WHEN type = 'R' THEN 1 ELSE 0 END, name`)
	if err != nil {
		return nil, err
	}
	return driver.ScanPrincipals(rows)
}
