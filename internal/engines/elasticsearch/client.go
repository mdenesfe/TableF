package elasticsearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
)

// DriverName is the database/sql name of the Elasticsearch SQL driver.
const DriverName = "tablef-elasticsearch"

func init() { sql.Register(DriverName, sqlDriver{}) }

// dsn is the JSON form accepted by sql.Open(DriverName, ...). TableF opens
// through a Connector so credentials never sit in a DSN string.
type dsn struct {
	BaseURL            string `json:"baseUrl"`
	User               string `json:"user,omitempty"`
	Password           string `json:"password,omitempty"`
	APIKey             string `json:"apiKey,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
	FetchSize          int    `json:"fetchSize,omitempty"`
}

type client struct {
	base      string
	user      string
	password  string
	apiKey    string
	fetchSize int
	http      *http.Client
}

func newClient(d dsn) (*client, error) {
	if d.BaseURL == "" {
		return nil, errors.New("elasticsearch: host is required")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if d.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for self-signed clusters
	}
	fs := d.FetchSize
	if fs <= 0 {
		fs = 1000
	}
	return &client{
		base: strings.TrimRight(d.BaseURL, "/"), user: d.User, password: d.Password, apiKey: d.APIKey,
		fetchSize: fs, http: &http.Client{Transport: tr, Timeout: 120 * time.Second},
	}, nil
}

// esError is Elasticsearch's error envelope.
type esError struct {
	Error json.RawMessage `json:"error"`
}

func errorMessage(status int, body []byte) error {
	var e esError
	if json.Unmarshal(body, &e) == nil && len(e.Error) > 0 {
		var obj struct {
			Type      string `json:"type"`
			Reason    string `json:"reason"`
			RootCause []struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"root_cause"`
		}
		if json.Unmarshal(e.Error, &obj) == nil {
			reason, typ := obj.Reason, obj.Type
			if len(obj.RootCause) > 0 && obj.RootCause[0].Reason != "" {
				reason, typ = obj.RootCause[0].Reason, obj.RootCause[0].Type
			}
			if reason != "" {
				if typ != "" {
					return fmt.Errorf("elasticsearch: %s: %s", typ, reason)
				}
				return errors.New("elasticsearch: " + reason)
			}
		}
		var s string
		if json.Unmarshal(e.Error, &s) == nil && s != "" {
			return errors.New("elasticsearch: " + s)
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		s = http.StatusText(status)
	}
	return fmt.Errorf("elasticsearch: HTTP %d: %s", status, s)
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch {
	case c.apiKey != "":
		req.Header.Set("Authorization", "ApiKey "+c.apiKey)
	case c.user != "":
		req.SetBasicAuth(c.user, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("elasticsearch: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return fmt.Errorf("elasticsearch: reading response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return errorMessage(resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("elasticsearch: invalid response: %w", err)
	}
	return nil
}

type sqlColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type sqlResponse struct {
	Columns []sqlColumn `json:"columns"`
	Rows    [][]any     `json:"rows"`
	Cursor  string      `json:"cursor"`
}

func (c *client) sql(ctx context.Context, q string, args []sqldriver.NamedValue) (*sqlResponse, error) {
	body := map[string]any{"query": q, "fetch_size": c.fetchSize, "field_multi_value_leniency": true}
	if len(args) > 0 {
		params := make([]any, len(args))
		for i, a := range args {
			if a.Name != "" {
				return nil, errors.New("elasticsearch: named parameters are not supported, use ?")
			}
			params[i] = jsonParam(a.Value)
		}
		body["params"] = params
	}
	var r sqlResponse
	if err := c.do(ctx, http.MethodPost, "/_sql?format=json", body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *client) next(ctx context.Context, cursor string) (*sqlResponse, error) {
	var r sqlResponse
	if err := c.do(ctx, http.MethodPost, "/_sql?format=json", map[string]any{"cursor": cursor}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *client) closeCursor(cursor string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.do(ctx, http.MethodPost, "/_sql/close", map[string]any{"cursor": cursor}, nil)
}

type rootInfo struct {
	ClusterName string `json:"cluster_name"`
	Version     struct {
		Number       string `json:"number"`
		Distribution string `json:"distribution"`
	} `json:"version"`
}

func (c *client) root(ctx context.Context) (rootInfo, error) {
	var r rootInfo
	err := c.do(ctx, http.MethodGet, "/", nil, &r)
	return r, err
}

func jsonParam(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	}
	return v
}

// value converts a decoded JSON cell (numbers as json.Number) to a driver.Value.
func value(v any) any {
	switch x := v.(type) {
	case nil, string, bool:
		return x
	case json.Number:
		if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return string(x)
	default: // objects and arrays (geo points, multi-values)
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// ---- query rewriting ----
//
// Elasticsearch SQL can't run two shapes the core uses for grouping: an
// ORDER BY on a CASE expression over a grouping key, and an aggregate over a
// sub-select. The first is dropped (NULL buckets then sort by count like the
// rest); the second is computed here from the inner GROUP BY's rows.

var (
	nullsLastOrder = regexp.MustCompile(`(?i)ORDER BY CASE WHEN .+? IS NULL THEN 1 ELSE 0 END,\s*`)
	groupTotals    = regexp.MustCompile(`(?is)^\s*SELECT COUNT\(\*\),\s*COALESCE\(SUM\(n\),\s*0\)\s+FROM\s+\((SELECT COUNT\(\*\) AS n FROM .+ GROUP BY .+)\)\s+g\s*$`)
)

func rewrite(q string) string { return nullsLastOrder.ReplaceAllString(q, "ORDER BY ") }

// ---- database/sql driver ----

type sqlDriver struct{}

func (sqlDriver) Open(name string) (sqldriver.Conn, error) {
	c, err := sqlDriver{}.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}

func (sqlDriver) OpenConnector(name string) (sqldriver.Connector, error) {
	var d dsn
	if err := json.Unmarshal([]byte(name), &d); err != nil {
		return nil, errors.New("elasticsearch: DSN must be JSON with baseUrl")
	}
	cl, err := newClient(d)
	if err != nil {
		return nil, err
	}
	return connector{cl}, nil
}

type connector struct{ c *client }

func (c connector) Connect(context.Context) (sqldriver.Conn, error) { return &conn{c.c}, nil }
func (connector) Driver() sqldriver.Driver                          { return sqlDriver{} }

type conn struct{ c *client }

var errNoTx = errors.New("elasticsearch: transactions are not supported")

func (c *conn) Prepare(q string) (sqldriver.Stmt, error) { return &stmt{c, q}, nil }
func (c *conn) Close() error                             { return nil }
func (c *conn) Begin() (sqldriver.Tx, error)             { return nil, errNoTx }
func (c *conn) BeginTx(context.Context, sqldriver.TxOptions) (sqldriver.Tx, error) {
	return nil, errNoTx
}

func (c *conn) Ping(ctx context.Context) error {
	_, err := c.c.root(ctx)
	return err
}

func (c *conn) CheckNamedValue(nv *sqldriver.NamedValue) error {
	v, err := sqldriver.DefaultParameterConverter.ConvertValue(nv.Value)
	if err != nil {
		return err
	}
	nv.Value = v
	return nil
}

func (c *conn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	if m := groupTotals.FindStringSubmatch(q); m != nil {
		return c.groupTotals(ctx, m[1], args)
	}
	r, err := c.c.sql(ctx, rewrite(q), args)
	if err != nil {
		return nil, err
	}
	return &rows{ctx: ctx, c: c.c, cols: r.Columns, page: r.Rows, cursor: r.Cursor}, nil
}

// maxGroupBuckets caps how many buckets groupTotals reads. Past it the
// totals are lower bounds: they count only the first maxGroupBuckets groups,
// which keeps a high-cardinality column (e.g. an id) from paging through
// millions of buckets just to show a count.
const maxGroupBuckets = 100_000

// groupTotals answers "how many groups, how many rows" by reading the
// buckets (Elasticsearch SQL can't nest the aggregation), up to
// maxGroupBuckets of them.
func (c *conn) groupTotals(ctx context.Context, inner string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	r, err := c.QueryContext(ctx, inner, args)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var groups, total int64
	dest := make([]sqldriver.Value, len(r.Columns()))
	for groups < maxGroupBuckets {
		if err := r.Next(dest); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		groups++
		if n, ok := dest[0].(int64); ok {
			total += n
		}
	}
	return &rows{cols: []sqlColumn{{Name: "groups", Type: "long"}, {Name: "rows", Type: "long"}},
		page: [][]any{{json.Number(strconv.FormatInt(groups, 10)), json.Number(strconv.FormatInt(total, 10))}}}, nil
}

// ExecContext runs the statement and discards its rows; the SQL API only
// reads, so writes fail with Elasticsearch's own error.
func (c *conn) ExecContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	r, err := c.c.sql(ctx, rewrite(q), args)
	if err != nil {
		return nil, err
	}
	if r.Cursor != "" {
		c.c.closeCursor(r.Cursor)
	}
	return sqldriver.RowsAffected(0), nil
}

type stmt struct {
	c *conn
	q string
}

func (s *stmt) Close() error  { return nil }
func (s *stmt) NumInput() int { return -1 }
func (s *stmt) Exec(args []sqldriver.Value) (sqldriver.Result, error) {
	return s.c.ExecContext(context.Background(), s.q, driver.NamedValues(args))
}
func (s *stmt) Query(args []sqldriver.Value) (sqldriver.Rows, error) {
	return s.c.QueryContext(context.Background(), s.q, driver.NamedValues(args))
}
func (s *stmt) ExecContext(ctx context.Context, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	return s.c.ExecContext(ctx, s.q, args)
}
func (s *stmt) QueryContext(ctx context.Context, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	return s.c.QueryContext(ctx, s.q, args)
}

// rows pages through results lazily: the next cursor page is fetched only
// when the caller reads past the current one, and an unfinished cursor is
// closed on Close.
type rows struct {
	ctx    context.Context
	c      *client
	cols   []sqlColumn
	page   [][]any
	i      int
	cursor string
}

func (r *rows) Columns() []string {
	out := make([]string, len(r.cols))
	for i, c := range r.cols {
		out[i] = c.Name
	}
	return out
}

func (r *rows) ColumnTypeDatabaseTypeName(i int) string { return strings.ToUpper(r.cols[i].Type) }

func (r *rows) Close() error {
	if r.cursor != "" {
		r.c.closeCursor(r.cursor)
		r.cursor = ""
	}
	return nil
}

func (r *rows) Next(dest []sqldriver.Value) error {
	for r.i >= len(r.page) {
		if r.cursor == "" {
			return io.EOF
		}
		ctx := r.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		next, err := r.c.next(ctx, r.cursor)
		if err != nil {
			return err
		}
		r.page, r.i, r.cursor = next.Rows, 0, next.Cursor
	}
	row := r.page[r.i]
	r.i++
	for i := range dest {
		if i < len(row) {
			dest[i] = value(row[i])
		} else {
			dest[i] = nil
		}
	}
	return nil
}
