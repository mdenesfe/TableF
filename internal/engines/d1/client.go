package d1

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
)

// DriverName is the database/sql name of the D1 HTTP driver.
const DriverName = "tablef-d1"

const defaultAPIBase = "https://api.cloudflare.com/client/v4"

func init() { sql.Register(DriverName, sqlDriver{}) }

// dsn is what a sql.Open(DriverName, ...) DSN decodes to (JSON). TableF opens
// through a Connector instead, so the token never sits in a DSN string.
type dsn struct {
	AccountID  string `json:"accountId"`
	DatabaseID string `json:"databaseId"`
	APIToken   string `json:"apiToken"`
	APIBase    string `json:"apiBase,omitempty"`
	Name       string `json:"name,omitempty"`
}

type client struct {
	endpoint string // .../query
	token    string
	name     string // what Databases reports
	http     *http.Client
}

func newClient(d dsn) (*client, error) {
	if d.AccountID == "" || d.DatabaseID == "" || d.APIToken == "" {
		return nil, errors.New("d1: accountId, databaseId and apiToken are required")
	}
	base := strings.TrimRight(d.APIBase, "/")
	if base == "" {
		base = defaultAPIBase
	}
	name := d.Name
	if name == "" {
		name = d.DatabaseID
	}
	return &client{
		endpoint: fmt.Sprintf("%s/accounts/%s/d1/database/%s/query", base, url.PathEscape(d.AccountID), url.PathEscape(d.DatabaseID)),
		token:    d.APIToken,
		name:     name,
		http:     &http.Client{Timeout: 90 * time.Second},
	}, nil
}

// result is one statement's outcome, columns in server order.
type result struct {
	columns   []string
	rows      [][]any
	changes   int64
	lastRowID int64
}

type apiResponse struct {
	Success bool       `json:"success"`
	Errors  []apiError `json:"errors"`
	Result  []struct {
		Results json.RawMessage `json:"results"`
		Success *bool           `json:"success"`
		Error   string          `json:"error"`
		Meta    struct {
			Changes   int64 `json:"changes"`
			LastRowID int64 `json:"last_row_id"`
		} `json:"meta"`
	} `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *client) query(ctx context.Context, q string, args []sqldriver.NamedValue) ([]result, error) {
	params := make([]any, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, errors.New("d1: named parameters are not supported, use ?")
		}
		params[i] = jsonParam(a.Value)
	}
	body, err := json.Marshal(map[string]any{"sql": q, "params": params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("d1: %v", uerr.Err) // the URL has no secrets, but keep errors short
		}
		return nil, fmt.Errorf("d1: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, fmt.Errorf("d1: reading response: %w", err)
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("d1: HTTP %d: %s", resp.StatusCode, snippet(raw))
		}
		return nil, fmt.Errorf("d1: invalid response: %w", err)
	}
	if !ar.Success || resp.StatusCode >= 300 {
		msgs := make([]string, 0, len(ar.Errors))
		for _, e := range ar.Errors {
			msgs = append(msgs, e.Message)
		}
		for _, r := range ar.Result {
			if r.Error != "" {
				msgs = append(msgs, r.Error)
			}
		}
		if len(msgs) == 0 {
			msgs = append(msgs, fmt.Sprintf("HTTP %d", resp.StatusCode))
		}
		return nil, errors.New("d1: " + strings.Join(msgs, "; "))
	}
	out := make([]result, 0, len(ar.Result))
	for _, r := range ar.Result {
		if r.Success != nil && !*r.Success {
			msg := r.Error
			if msg == "" {
				msg = "statement failed"
			}
			return nil, errors.New("d1: " + msg)
		}
		res, err := decodeResults(r.Results)
		if err != nil {
			return nil, fmt.Errorf("d1: decoding results: %w", err)
		}
		res.changes, res.lastRowID = r.Meta.Changes, r.Meta.LastRowID
		out = append(out, res)
	}
	return out, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// jsonParam maps a database/sql value to something D1 accepts in params.
func jsonParam(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
	}
	return v
}

// decodeResults reads the "results" member. The /query endpoint returns an
// array of objects; JSON objects are unordered once in a Go map, so they are
// read token by token to keep the server's column order. The /raw shape
// ({"columns": [...], "rows": [[...]]}) is accepted too.
func decodeResults(raw json.RawMessage) (result, error) {
	var res result
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return res, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return res, err
	}
	switch tok {
	case json.Delim('{'):
		var shape struct {
			Columns []string            `json:"columns"`
			Rows    [][]json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(raw, &shape); err != nil {
			return res, err
		}
		res.columns = shape.Columns
		for _, r := range shape.Rows {
			row := make([]any, len(r))
			for i, v := range r {
				if row[i], err = jsonValue(v); err != nil {
					return res, err
				}
			}
			res.rows = append(res.rows, row)
		}
		return res, nil
	case json.Delim('['):
	default:
		return res, fmt.Errorf("unexpected token %v", tok)
	}
	index := map[string]int{}
	for dec.More() {
		if t, err := dec.Token(); err != nil {
			return res, err
		} else if t != json.Delim('{') {
			return res, fmt.Errorf("expected object, got %v", t)
		}
		row := make([]any, len(res.columns))
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return res, err
			}
			key, _ := kt.(string)
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				return res, err
			}
			val, err := jsonValue(v)
			if err != nil {
				return res, err
			}
			i, ok := index[key]
			if !ok { // first sighting: append the column, backfill earlier rows
				i = len(res.columns)
				index[key] = i
				res.columns = append(res.columns, key)
				for j := range res.rows {
					res.rows[j] = append(res.rows[j], nil)
				}
				row = append(row, nil)
			}
			row[i] = val
		}
		if _, err := dec.Token(); err != nil { // '}'
			return res, err
		}
		res.rows = append(res.rows, row)
	}
	return res, nil
}

// jsonValue converts one JSON value into a database/sql driver.Value.
func jsonValue(raw json.RawMessage) (any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	switch raw[0] {
	case 'n':
		return nil, nil
	case 't':
		return true, nil
	case 'f':
		return false, nil
	case '"':
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err
	case '{', '[': // D1 returns BLOBs as arrays of bytes
		var ints []int
		if raw[0] == '[' && json.Unmarshal(raw, &ints) == nil && isBytes(ints) {
			bs := make([]byte, len(ints))
			for i, n := range ints {
				bs[i] = byte(n)
			}
			return bs, nil
		}
		return string(raw), nil
	default:
		s := string(raw)
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err
	}
}

func isBytes(ns []int) bool {
	for _, n := range ns {
		if n < 0 || n > 255 {
			return false
		}
	}
	return true
}

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
		return nil, errors.New("d1: DSN must be JSON with accountId, databaseId and apiToken")
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

var errNoTx = errors.New("d1: transactions are not supported over the HTTP API")

func (c *conn) Prepare(q string) (sqldriver.Stmt, error) { return &stmt{c, q}, nil }
func (c *conn) Close() error                             { return nil }
func (c *conn) Begin() (sqldriver.Tx, error)             { return nil, errNoTx }
func (c *conn) BeginTx(context.Context, sqldriver.TxOptions) (sqldriver.Tx, error) {
	return nil, errNoTx
}

func (c *conn) Ping(ctx context.Context) error {
	_, err := c.c.query(ctx, "SELECT 1", nil)
	return err
}

func (c *conn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	res, err := c.c.query(ctx, q, args)
	if err != nil {
		return nil, err
	}
	// Several statements in one call: show the last that returned columns.
	var r result
	for _, x := range res {
		if len(x.columns) > 0 || len(r.columns) == 0 {
			r = x
		}
	}
	return &rows{cols: r.columns, data: r.rows}, nil
}

func (c *conn) ExecContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	res, err := c.c.query(ctx, q, args)
	if err != nil {
		return nil, err
	}
	var out execResult
	for _, r := range res {
		out.changes += r.changes
		if r.lastRowID != 0 {
			out.lastID = r.lastRowID
		}
	}
	return out, nil
}

// CheckNamedValue accepts the standard driver values plus anything the
// default converter turns into one.
func (c *conn) CheckNamedValue(nv *sqldriver.NamedValue) error {
	v, err := sqldriver.DefaultParameterConverter.ConvertValue(nv.Value)
	if err != nil {
		return err
	}
	nv.Value = v
	return nil
}

type execResult struct{ changes, lastID int64 }

func (r execResult) LastInsertId() (int64, error) { return r.lastID, nil }
func (r execResult) RowsAffected() (int64, error) { return r.changes, nil }

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

type rows struct {
	cols []string
	data [][]any
	i    int
}

func (r *rows) Columns() []string {
	if r.cols == nil {
		return []string{}
	}
	return r.cols
}
func (r *rows) Close() error { return nil }
func (r *rows) Next(dest []sqldriver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	row := r.data[r.i]
	r.i++
	for i := range dest {
		if i < len(row) {
			dest[i] = row[i]
		} else {
			dest[i] = nil
		}
	}
	return nil
}
