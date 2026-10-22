package core

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Who the session runs as, asked of the server rather than read from the
// connection settings: a pooler, a role switch or a mapped login can make it
// differ from the configured user (Supabase's poolers, SET ROLE, Windows
// logins on SQL Server).
type identity struct {
	User      string // the role the session acts as
	Login     string // the login, when it differs from User
	Superuser bool   // superuser, sysadmin or account admin
}

// whoQueries return (user, login, superuser) for each dialect family.
var whoQueries = map[string]string{
	"postgres":   `SELECT current_user::text, session_user::text, COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false)`,
	"mysql":      `SELECT CURRENT_USER(), USER(), 0`,
	"mssql":      `SELECT SUSER_SNAME(), ORIGINAL_LOGIN(), CAST(IS_SRVROLEMEMBER('sysadmin') AS int)`,
	"oracle":     `SELECT USER, SYS_CONTEXT('USERENV', 'SESSION_USER'), 0 FROM dual`,
	"clickhouse": `SELECT currentUser(), currentUser(), 0`,
	"snowflake":  `SELECT CURRENT_USER(), CURRENT_USER(), IFF(CURRENT_ROLE() = 'ACCOUNTADMIN', 1, 0)`,
	"vertica":    `SELECT CURRENT_USER, SESSION_USER, CASE WHEN (SELECT is_super_user FROM users WHERE user_name = CURRENT_USER) THEN 1 ELSE 0 END`,
}

// whoAmI asks the server; when it can't (an engine without the concept, a
// query the server refuses) it falls back to the configured user.
func whoAmI(ctx context.Context, sess *session) identity {
	fallback := identity{User: sess.cfg.User}
	q, ok := whoQueries[sess.feat.Family]
	if !ok || sess.db == nil {
		return fallback
	}
	var user, login sql.NullString
	var super sql.NullInt64
	var superBool sql.NullBool
	row := sess.db.QueryRowContext(ctx, q)
	if sess.feat.Family == "postgres" {
		if err := row.Scan(&user, &login, &superBool); err != nil {
			return fallback
		}
		super.Valid, super.Int64 = superBool.Valid, map[bool]int64{true: 1}[superBool.Bool]
	} else if err := row.Scan(&user, &login, &super); err != nil {
		return fallback
	}
	if user.String == "" {
		return fallback
	}
	id := identity{User: user.String, Superuser: super.Valid && super.Int64 == 1}
	if login.String != "" && login.String != user.String {
		id.Login = login.String
	}
	return id
}

// ListRoles lists the roles the session's login may act as (PostgreSQL);
// engines without role switching return none.
func (s *Service) ListRoles(id string) ([]string, error) {
	sess, err := s.get(id)
	if err != nil {
		return nil, err
	}
	rs, ok := sess.drv.(driver.RoleSwitcher)
	if sess.store != nil || !ok {
		return []string{}, nil
	}
	ctx, cancel := s.withTimeout(15 * time.Second)
	defer cancel()
	return rs.Roles(ctx, sess.db)
}

// UseRole reconnects the session acting as role (SET ROLE on every
// connection); "" goes back to the login's own role. The saved connection is
// not changed. When the server refuses the role, the session stays as it was.
func (s *Service) UseRole(id, role string) (SessionInfo, error) {
	sess, err := s.get(id)
	if err != nil {
		return SessionInfo{}, err
	}
	if _, ok := sess.drv.(driver.RoleSwitcher); !ok || sess.store != nil {
		return SessionInfo{}, errors.New("this database can't switch roles")
	}
	cfg := sess.cfg
	cfg.Options = maps.Clone(cfg.Options)
	if cfg.Options == nil {
		cfg.Options = map[string]string{}
	}
	if role == "" {
		delete(cfg.Options, driver.RoleOption)
	} else {
		cfg.Options[driver.RoleOption] = role
	}
	return s.connect(cfg)
}

// ConnectAs reconnects the session as another user of the same server, for
// this session only: the saved connection keeps its user and password. An
// empty password reuses the current one. The session stays as it was when
// the new login fails.
func (s *Service) ConnectAs(id, user, password string) (SessionInfo, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return SessionInfo{}, errors.New("a user name is required")
	}
	sess, err := s.get(id)
	if err != nil {
		return SessionInfo{}, err
	}
	cfg := sess.cfg
	cfg.User = user
	if password != "" {
		cfg.Password = password
	}
	cfg.Options = maps.Clone(cfg.Options)
	delete(cfg.Options, driver.RoleOption) // a role belongs to the previous login
	return s.connect(cfg)
}
