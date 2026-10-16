// Package elasticsearch adds Elasticsearch through its SQL REST API (read-only).
package elasticsearch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/mdenesfe/TableF/internal/driver"
)

type esDriver struct {
	driver.NoForeignKeys
	driver.NoRoutines
}

func init() { driver.Register(esDriver{}) }

func (esDriver) Name() string { return "elasticsearch" }

// Features: the SQL API only reads (no INSERT/UPDATE/DELETE, no transactions),
// has no schemas, foreign keys or routines. GROUP BY and COUNT(*) work.
func (esDriver) Features() driver.Features {
	return driver.Features{Family: "elasticsearch", Groups: true}
}

func baseURL(cfg driver.Config) (string, error) {
	host := strings.TrimSpace(cfg.Host)
	scheme := strings.ToLower(cfg.Opt("scheme", "http"))
	if strings.Contains(host, "://") { // a full URL typed into the host field
		u, err := url.Parse(host)
		if err != nil {
			return "", fmt.Errorf("elasticsearch: invalid host: %w", err)
		}
		scheme, host = u.Scheme, u.Host
		if h, p, err := net.SplitHostPort(host); err == nil {
			host = h
			if cfg.Port == 0 {
				cfg.Port, _ = strconv.Atoi(p)
			}
		}
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("elasticsearch: scheme must be http or https, got %q", scheme)
	}
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = driver.DefaultPort("elasticsearch")
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func dsnOf(cfg driver.Config) (dsn, error) {
	base, err := baseURL(cfg)
	if err != nil {
		return dsn{}, err
	}
	fs, _ := strconv.Atoi(cfg.Opt("fetchSize", ""))
	return dsn{
		BaseURL: base, User: cfg.User, Password: cfg.Password, APIKey: cfg.Opt("apiKey", ""),
		InsecureSkipVerify: strings.EqualFold(cfg.Opt("insecureSkipVerify", ""), "true"),
		FetchSize:          fs,
	}, nil
}

func (esDriver) Open(cfg driver.Config) (*sql.DB, error) {
	d, err := dsnOf(cfg)
	if err != nil {
		return nil, err
	}
	c, err := newClient(d)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector{c}), nil
}

func clientOf(ctx context.Context, db *sql.DB) (*client, error) {
	cn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer cn.Close()
	var cl *client
	err = cn.Raw(func(dc any) error {
		c, ok := dc.(*conn)
		if !ok {
			return errors.New("elasticsearch: not an Elasticsearch connection")
		}
		cl = c.c
		return nil
	})
	return cl, err
}

// Databases returns the cluster name: one cluster is one "database".
func (esDriver) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	cl, err := clientOf(ctx, db)
	if err != nil {
		return nil, err
	}
	info, err := cl.root(ctx)
	if err != nil {
		return nil, err
	}
	return []string{info.ClusterName}, nil
}

func (esDriver) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	if cl, err := clientOf(ctx, db); err == nil {
		if info, err := cl.root(ctx); err == nil {
			return info.ClusterName
		}
	}
	return ""
}

func (esDriver) Version(ctx context.Context, db *sql.DB) string {
	cl, err := clientOf(ctx, db)
	if err != nil {
		return ""
	}
	info, err := cl.root(ctx)
	if err != nil || info.Version.Number == "" {
		return ""
	}
	name := "Elasticsearch"
	if strings.EqualFold(info.Version.Distribution, "opensearch") {
		name = "OpenSearch"
	}
	return name + " " + info.Version.Number
}

func (esDriver) Schemas(context.Context, *sql.DB) ([]string, error) { return []string{}, nil }

// Tables lists indices, aliases and data streams from SHOW TABLES. Older
// versions have no catalog column, so columns are matched by name.
func (esDriver) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx, "SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	nameAt, typeAt := -1, -1
	for i, c := range cols {
		switch strings.ToLower(c) {
		case "name":
			nameAt = i
		case "type":
			typeAt = i
		}
	}
	if nameAt < 0 {
		return nil, errors.New("elasticsearch: SHOW TABLES returned no name column")
	}
	out := []driver.Table{}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		name := fmt.Sprint(vals[nameAt])
		if name == "" || strings.HasPrefix(name, ".") {
			continue // hidden/system indices
		}
		typ := "table"
		if typeAt >= 0 {
			switch strings.ToUpper(fmt.Sprint(vals[typeAt])) {
			case "VIEW", "ALIAS":
				typ = "view"
			}
		}
		out = append(out, driver.Table{Name: name, Type: typ})
	}
	return out, rows.Err()
}

// Columns are taken from the result of SELECT * itself, so they line up with
// the rows the core loads (SHOW COLUMNS also lists object containers and
// multi-fields that SELECT * leaves out). SHOW COLUMNS is the fallback.
func (d esDriver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	from := driver.QualifiedName(d, schema, table)
	for _, lim := range []int{0, 1} {
		rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT %d", from, lim))
		if err != nil {
			continue
		}
		types, err := rows.ColumnTypes()
		rows.Close()
		if err != nil || len(types) == 0 {
			continue
		}
		out := make([]driver.Column, len(types))
		for i, t := range types {
			out[i] = driver.Column{Name: t.Name(), Type: t.DatabaseTypeName(), Nullable: true}
		}
		return out, nil
	}
	return d.showColumns(ctx, db, from)
}

func (esDriver) showColumns(ctx context.Context, db *sql.DB, from string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx, "SHOW COLUMNS FROM "+from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	out := []driver.Column{}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		if len(vals) < 2 {
			continue
		}
		typ := strings.ToUpper(fmt.Sprint(vals[1]))
		if typ == "STRUCT" || typ == "NESTED" {
			continue
		}
		out = append(out, driver.Column{Name: fmt.Sprint(vals[0]), Type: typ, Nullable: true})
	}
	return out, rows.Err()
}

func (esDriver) QuoteIdent(name string) string { return driver.QuoteWith(name, `"`) }
func (esDriver) Placeholder(int) string        { return "?" }

// DefaultSchema is empty so table names are never qualified.
func (esDriver) DefaultSchema(driver.Config) string { return "" }

// Paginate: Elasticsearch SQL has LIMIT but no OFFSET, so fetch limit+offset
// rows and let the core drop the first offset.
func (esDriver) Paginate(q string, limit, offset int) (string, int) {
	return fmt.Sprintf("%s LIMIT %d", q, limit+offset), offset
}
