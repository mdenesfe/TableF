package bigquery

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	bq "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/mdenesfe/TableF/internal/driver"
)

// DriverName is the database/sql driver name. DSNs are not supported:
// connections are made through sql.OpenDB with a connector built from a Config.
const DriverName = "tablef-bigquery"

func init() { sql.Register(DriverName, sqlDriver{}) }

type sqlDriver struct{}

func (sqlDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errors.New("tablef-bigquery: use sql.OpenDB with a connector")
}

// client is shared by every database/sql connection of one sql.DB.
type client struct {
	bq        *bigquery.Client
	raw       *bq.Service // REST API, for catalog calls the client library hides (table types)
	project   string
	dataset   string // default dataset for unqualified table names
	maxBilled int64
}

// credentialOption picks the credentials type from the JSON "type" field so
// the non-deprecated typed options can be used.
func credentialOption(data []byte, file string) (option.ClientOption, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("bigquery: credentials are not valid JSON: %w", err)
	}
	var t option.CredentialsType
	switch head.Type {
	case "service_account":
		t = option.ServiceAccount
	case "authorized_user":
		t = option.AuthorizedUser
	case "external_account":
		t = option.ExternalAccount
	case "impersonated_service_account":
		t = option.ImpersonatedServiceAccount
	default:
		return nil, fmt.Errorf("bigquery: unsupported credentials type %q", head.Type)
	}
	if file != "" {
		return option.WithAuthCredentialsFile(t, file), nil
	}
	return option.WithAuthCredentialsJSON(t, data), nil
}

func clientOptions(cfg driver.Config) ([]option.ClientOption, error) {
	if js := strings.TrimSpace(cfg.Opt("credentialsJSON", "")); js != "" {
		o, err := credentialOption([]byte(js), "")
		if err != nil {
			return nil, err
		}
		return []option.ClientOption{o}, nil
	}
	if path := strings.TrimSpace(cfg.Opt("credentialsFile", "")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("bigquery: reading credentials file: %w", err)
		}
		o, err := credentialOption(data, path)
		if err != nil {
			return nil, err
		}
		return []option.ClientOption{o}, nil
	}
	return nil, nil // Application Default Credentials
}

// defaultDataset is cfg.Database, except that picking the project itself from
// the database list (Databases returns the project id) means "no default".
func defaultDataset(cfg driver.Config) string {
	project := strings.TrimSpace(cfg.Opt("projectId", ""))
	ds := strings.TrimSpace(cfg.Database)
	if ds == project {
		return ""
	}
	return ds
}

func newClient(cfg driver.Config) (*client, error) {
	project := strings.TrimSpace(cfg.Opt("projectId", ""))
	if project == "" {
		return nil, errors.New("bigquery: the projectId option is required")
	}
	opts, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	c, err := bigquery.NewClient(ctx, project, opts...)
	if err != nil {
		return nil, err
	}
	c.Location = strings.TrimSpace(cfg.Opt("location", ""))
	raw, err := bq.NewService(ctx, opts...)
	if err != nil {
		c.Close()
		return nil, err
	}
	cl := &client{bq: c, raw: raw, project: project, dataset: defaultDataset(cfg)}
	if s := cfg.Opt("maxBytesBilled", ""); s != "" {
		if cl.maxBilled, err = strconv.ParseInt(s, 10, 64); err != nil {
			c.Close()
			return nil, fmt.Errorf("bigquery: maxBytesBilled: %w", err)
		}
	}
	return cl, nil
}

func (c *client) newQuery(q string, args []sqldriver.NamedValue) (*bigquery.Query, error) {
	vals := make([]any, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	q, params, err := bind(q, vals)
	if err != nil {
		return nil, err
	}
	query := c.bq.Query(q)
	query.Parameters = params
	query.DefaultProjectID = c.project
	query.DefaultDatasetID = c.dataset
	query.MaxBytesBilled = c.maxBilled
	return query, nil
}

type connector struct{ c *client }

func (k connector) Connect(context.Context) (sqldriver.Conn, error) { return &conn{c: k.c}, nil }
func (k connector) Driver() sqldriver.Driver                        { return sqlDriver{} }
func (k connector) Close() error                                    { return k.c.bq.Close() }

var _ io.Closer = connector{}

type conn struct{ c *client }

var (
	_ sqldriver.QueryerContext     = (*conn)(nil)
	_ sqldriver.ExecerContext      = (*conn)(nil)
	_ sqldriver.Pinger             = (*conn)(nil)
	_ sqldriver.NamedValueChecker  = (*conn)(nil)
	_ sqldriver.ConnPrepareContext = (*conn)(nil)
)

func (c *conn) Prepare(q string) (sqldriver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}
func (c *conn) PrepareContext(_ context.Context, q string) (sqldriver.Stmt, error) {
	return &stmt{c: c, q: q}, nil
}
func (c *conn) Close() error { return nil }
func (c *conn) Begin() (sqldriver.Tx, error) {
	return nil, errors.New("bigquery: transactions are not supported by TableF's BigQuery driver")
}
func (c *conn) CheckNamedValue(*sqldriver.NamedValue) error { return nil }

// Ping lists at most one dataset: it checks credentials and project access
// without running (or billing) a query.
func (c *conn) Ping(ctx context.Context) error {
	it := c.c.bq.Datasets(ctx)
	it.PageInfo().MaxSize = 1
	if _, err := it.Next(); err != nil && !errors.Is(err, iterator.Done) {
		return err
	}
	return nil
}

// cancelJob cancels job in BigQuery if ctx ends before stop is called.
// Without it a query abandoned by the UI keeps running (and billing) on the
// server: canceling the context only stops the client from waiting.
func cancelJob(ctx context.Context, job *bigquery.Job) (stop func() bool) {
	return context.AfterFunc(ctx, func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = job.Cancel(cctx)
	})
}

func (c *conn) ExecContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	query, err := c.c.newQuery(q, args)
	if err != nil {
		return nil, err
	}
	job, err := query.Run(ctx)
	if err != nil {
		return nil, err
	}
	stop := cancelJob(ctx, job)
	defer stop()
	status, err := job.Wait(ctx)
	if err != nil {
		return nil, err
	}
	if err := status.Err(); err != nil {
		return nil, err
	}
	var n int64
	if status.Statistics != nil {
		if qs, ok := status.Statistics.Details.(*bigquery.QueryStatistics); ok {
			n = qs.NumDMLAffectedRows
		}
	}
	return result(n), nil
}

// QueryContext runs q as a query job. Queries are billed by bytes scanned,
// and LIMIT does not reduce that; see also tableCount.
func (c *conn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	if len(args) == 0 {
		if n, ok := c.c.tableCount(ctx, q); ok {
			return &countRows{n: n}, nil
		}
	}
	query, err := c.c.newQuery(q, args)
	if err != nil {
		return nil, err
	}
	job, err := query.Run(ctx)
	if err != nil {
		return nil, err
	}
	// Read waits for the job; once it is done there is nothing left to cancel.
	stop := cancelJob(ctx, job)
	it, err := job.Read(ctx)
	stop()
	if err != nil {
		return nil, err
	}
	r := &rows{it: it}
	// The schema is known once the first page is fetched.
	var first []bigquery.Value
	switch err := it.Next(&first); {
	case err == nil:
		r.first, r.hasFirst = first, true
	case errors.Is(err, iterator.Done):
		r.done = true
	default:
		return nil, err
	}
	r.schema = it.Schema
	return r, nil
}

// countAll matches the unfiltered total the table browser asks for:
// SELECT COUNT(*) FROM `dataset`.`table` (or just `table`).
var countAll = regexp.MustCompile("^SELECT COUNT\\(\\*\\) FROM (?:`((?:[^`\\\\]|\\\\.)+)`\\.)?`((?:[^`\\\\]|\\\\.)+)`$")

var unquote = strings.NewReplacer("\\`", "`", "\\\\", "\\")

// tableCount answers an unfiltered COUNT(*) of a regular table from its
// metadata (numRows), which is free, instead of running a billed query.
// Views, external tables and tables with a streaming buffer (whose rows are
// not in numRows yet) still run the query.
func (c *client) tableCount(ctx context.Context, q string) (int64, bool) {
	m := countAll.FindStringSubmatch(q)
	if m == nil {
		return 0, false
	}
	ds, table := unquote.Replace(m[1]), unquote.Replace(m[2])
	if ds == "" {
		ds = c.dataset
	}
	if ds == "" {
		return 0, false
	}
	md, err := c.bq.Dataset(ds).Table(table).Metadata(ctx)
	if err != nil || md.Type != bigquery.RegularTable || md.StreamingBuffer != nil || md.NumRows > math.MaxInt64 {
		return 0, false
	}
	return int64(md.NumRows), true
}

// countRows is the one-row, one-column result of tableCount.
type countRows struct {
	n    int64
	done bool
}

func (*countRows) Columns() []string                     { return []string{"f0_"} }
func (*countRows) ColumnTypeDatabaseTypeName(int) string { return "INT64" }
func (*countRows) Close() error                          { return nil }
func (r *countRows) Next(dest []sqldriver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.n
	return nil
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

// result reports NumDMLAffectedRows from the job statistics (0 for DDL).
type result int64

func (result) LastInsertId() (int64, error)   { return 0, errors.New("bigquery: no insert ids") }
func (r result) RowsAffected() (int64, error) { return int64(r), nil }

type rows struct {
	it       *bigquery.RowIterator
	schema   bigquery.Schema
	first    []bigquery.Value
	hasFirst bool
	done     bool
}

func (r *rows) Columns() []string {
	out := make([]string, len(r.schema))
	for i, f := range r.schema {
		out[i] = f.Name
	}
	return out
}

func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	if i < len(r.schema) {
		return typeName(r.schema[i])
	}
	return ""
}

func (r *rows) Close() error { r.done = true; return nil }

func (r *rows) Next(dest []sqldriver.Value) error {
	if r.done {
		return io.EOF
	}
	var row []bigquery.Value
	if r.hasFirst {
		row, r.hasFirst, r.first = r.first, false, nil
	} else if err := r.it.Next(&row); err != nil {
		if errors.Is(err, iterator.Done) {
			r.done = true
			return io.EOF
		}
		return err
	}
	fillRow(dest, row, r.schema)
	return nil
}

func fillRow(dest []sqldriver.Value, row []bigquery.Value, schema bigquery.Schema) {
	for i := range dest {
		dest[i] = nil
		if i < len(row) {
			var f *bigquery.FieldSchema
			if i < len(schema) {
				f = schema[i]
			}
			dest[i] = cell(row[i], f)
		}
	}
}

// withClient runs fn with the driver client behind db.
func withClient(ctx context.Context, db *sql.DB, fn func(*client) error) error {
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Raw(func(dc any) error {
		cn, ok := dc.(*conn)
		if !ok {
			return fmt.Errorf("bigquery: unexpected connection type %T", dc)
		}
		return fn(cn.c)
	})
}
