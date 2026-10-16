package dynamodb

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/mdenesfe/TableF/internal/driver"
)

// DriverName is the database/sql driver name. DSNs are not supported:
// connections are made through sql.OpenDB with a connector built from a Config.
const DriverName = "tablef-dynamodb"

func init() { sql.Register(DriverName, sqlDriver{}) }

type sqlDriver struct{}

func (sqlDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errors.New("tablef-dynamodb: use sql.OpenDB with a connector")
}

// api is the subset of the DynamoDB client the driver uses (faked in tests).
type api interface {
	ExecuteStatement(context.Context, *ddb.ExecuteStatementInput, ...func(*ddb.Options)) (*ddb.ExecuteStatementOutput, error)
	ListTables(context.Context, *ddb.ListTablesInput, ...func(*ddb.Options)) (*ddb.ListTablesOutput, error)
	DescribeTable(context.Context, *ddb.DescribeTableInput, ...func(*ddb.Options)) (*ddb.DescribeTableOutput, error)
	Scan(context.Context, *ddb.ScanInput, ...func(*ddb.Options)) (*ddb.ScanOutput, error)
}

// client is shared by every database/sql connection of one sql.DB.
type client struct {
	api    api
	region string
	mu     sync.Mutex
	keys   map[string][]string // table -> key attribute names (hash, range)
	keyTyp map[string]map[string]string
}

func newClient(a api, region string) *client {
	return &client{api: a, region: region, keys: map[string][]string{}, keyTyp: map[string]map[string]string{}}
}

func buildClient(cfg driver.Config) (*client, error) {
	region := strings.TrimSpace(cfg.Opt("region", ""))
	if region == "" {
		return nil, errors.New("dynamodb: the region option is required")
	}
	endpoint := strings.TrimSpace(cfg.Opt("endpoint", ""))
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if id := cfg.Opt("accessKeyId", ""); id != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(id, cfg.Opt("secretAccessKey", ""), cfg.Opt("sessionToken", ""))))
	} else if endpoint != "" {
		// DynamoDB Local accepts any credentials but the SDK insists on some.
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("local", "local", "")))
	}
	ac, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: %w", err)
	}
	c := ddb.NewFromConfig(ac, func(o *ddb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return newClient(c, region), nil
}

// keySchema returns the table's key attribute names (partition key first) and
// their types, caching DescribeTable.
func (c *client) keySchema(ctx context.Context, table string) ([]string, map[string]string, error) {
	c.mu.Lock()
	if k, ok := c.keys[table]; ok {
		t := c.keyTyp[table]
		c.mu.Unlock()
		return k, t, nil
	}
	c.mu.Unlock()
	out, err := c.api.DescribeTable(ctx, &ddb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		return nil, nil, err
	}
	keys, typ := keysOf(out.Table)
	c.mu.Lock()
	c.keys[table], c.keyTyp[table] = keys, typ
	c.mu.Unlock()
	return keys, typ, nil
}

func keysOf(t *types.TableDescription) ([]string, map[string]string) {
	keys := []string{}
	typ := map[string]string{}
	if t == nil {
		return keys, typ
	}
	ks := append([]types.KeySchemaElement(nil), t.KeySchema...)
	sort.SliceStable(ks, func(i, j int) bool { return ks[i].KeyType == types.KeyTypeHash && ks[j].KeyType != types.KeyTypeHash })
	for _, k := range ks {
		keys = append(keys, aws.ToString(k.AttributeName))
	}
	for _, d := range t.AttributeDefinitions {
		typ[aws.ToString(d.AttributeName)] = string(d.AttributeType)
	}
	return keys, typ
}

type connector struct{ c *client }

func (k connector) Connect(context.Context) (sqldriver.Conn, error) { return &conn{c: k.c}, nil }
func (k connector) Driver() sqldriver.Driver                        { return sqlDriver{} }

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
	return nil, errors.New("dynamodb: transactions are not supported")
}
func (c *conn) CheckNamedValue(*sqldriver.NamedValue) error { return nil }

func (c *conn) Ping(ctx context.Context) error {
	_, err := c.c.api.ListTables(ctx, &ddb.ListTablesInput{Limit: aws.Int32(1)})
	return err
}

func params(args []sqldriver.NamedValue) ([]types.AttributeValue, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]types.AttributeValue, len(args))
	for i, a := range args {
		v, err := toAttribute(a.Value)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (c *conn) ExecContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	q, _ = stripLimit(q)
	q, err := translateInsert(q)
	if err != nil {
		return nil, err
	}
	p, err := params(args)
	if err != nil {
		return nil, err
	}
	if _, err := c.c.api.ExecuteStatement(ctx, &ddb.ExecuteStatementInput{Statement: aws.String(q), Parameters: p}); err != nil {
		return nil, err
	}
	return result{}, nil
}

func (c *conn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	q, limit := stripLimit(q)
	p, err := params(args)
	if err != nil {
		return nil, err
	}
	if table, where, ok := countQuery(q); ok {
		n, err := c.c.count(ctx, table, where, p)
		if err != nil {
			return nil, err
		}
		return &staticRows{cols: []string{"count"}, vals: [][]sqldriver.Value{{n}}}, nil
	}
	q, err = translateInsert(q)
	if err != nil {
		return nil, err
	}
	return c.c.query(ctx, q, p, limit)
}

// approxCountThreshold: tables DescribeTable reports above this many items are
// not scanned for an exact total; the (approximate, refreshed about every six
// hours) ItemCount is returned instead.
const approxCountThreshold = 100_000

// count answers SELECT COUNT(*) FROM table [WHERE where], which PartiQL lacks.
// Without a condition it uses a Scan with Select=COUNT (exact) for small tables
// and DescribeTable's ItemCount for large ones. With a condition it runs the
// filtered SELECT, projecting only the key attributes, and counts the items.
func (c *client) count(ctx context.Context, tableRef, where string, p []types.AttributeValue) (int64, error) {
	table := tableName(tableRef)
	if where == "" {
		out, err := c.api.DescribeTable(ctx, &ddb.DescribeTableInput{TableName: aws.String(table)})
		if err != nil {
			return 0, err
		}
		if out.Table != nil {
			keys, typ := keysOf(out.Table)
			c.mu.Lock()
			c.keys[table], c.keyTyp[table] = keys, typ
			c.mu.Unlock()
			if n := aws.ToInt64(out.Table.ItemCount); n > approxCountThreshold {
				return n, nil
			}
		}
		var total int64
		var start map[string]types.AttributeValue
		for {
			s, err := c.api.Scan(ctx, &ddb.ScanInput{TableName: aws.String(table), Select: types.SelectCount, ExclusiveStartKey: start})
			if err != nil {
				return 0, err
			}
			total += int64(s.Count)
			if len(s.LastEvaluatedKey) == 0 {
				return total, nil
			}
			start = s.LastEvaluatedKey
		}
	}
	proj := "*"
	if keys, _, err := c.keySchema(ctx, table); err == nil && len(keys) > 0 {
		q := make([]string, len(keys))
		for i, k := range keys {
			q[i] = driver.QuoteWith(k, `"`)
		}
		proj = strings.Join(q, ", ")
	}
	stmt := "SELECT " + proj + " FROM " + tableRef + " WHERE " + where
	var total int64
	var token *string
	for {
		out, err := c.api.ExecuteStatement(ctx, &ddb.ExecuteStatementInput{Statement: aws.String(stmt), Parameters: p, NextToken: token})
		if err != nil {
			return 0, err
		}
		total += int64(len(out.Items))
		if out.NextToken == nil {
			return total, nil
		}
		token = out.NextToken
	}
}

// query runs a statement and presents its items as rows with a fixed column
// set: the projection list when the statement names plain attributes, else the
// table's key attributes followed by the union of attribute names found in the
// first non-empty page (sorted). Attributes that first appear on later pages
// are not shown; missing attributes are NULL. limit > 0 stops after that many
// items (paging, see translate.go).
func (c *client) query(ctx context.Context, q string, p []types.AttributeValue, limit int) (*rows, error) {
	r := &rows{c: c, ctx: ctx, stmt: q, params: p, limit: limit, kinds: kinds{}}
	if err := r.fetch(); err != nil {
		return nil, err
	}
	// Skip empty pages (filters can produce them) to discover the columns.
	for len(r.buf) == 0 && r.token != nil {
		if err := r.fetch(); err != nil {
			return nil, err
		}
	}
	if cols := projection(q); cols != nil {
		r.cols = cols
	} else {
		var keys []string
		var keyTyp map[string]string
		if t := statementTable(q); t != "" {
			keys, keyTyp, _ = c.keySchema(ctx, t) // best effort; columns still come from the items
		}
		r.cols = columnSet(keys, r.buf)
		for k, t := range keyTyp {
			r.kinds.add(k, t)
		}
	}
	return r, nil
}

// columnSet puts key attributes first, then every other attribute name seen in items.
func columnSet(keys []string, items []map[string]types.AttributeValue) []string {
	seen := map[string]bool{}
	cols := []string{}
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			cols = append(cols, k)
		}
	}
	var rest []string
	for _, it := range items {
		for name := range it {
			if !seen[name] {
				seen[name] = true
				rest = append(rest, name)
			}
		}
	}
	sort.Strings(rest)
	return append(cols, rest...)
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

// result: ExecuteStatement does not report how many items a write touched.
// RowsAffected returns an error so the core's "exactly one row" check is skipped.
type result struct{}

func (result) LastInsertId() (int64, error) { return 0, errors.New("dynamodb: no insert ids") }
func (result) RowsAffected() (int64, error) {
	return 0, errors.New("dynamodb: affected items are not reported")
}

type rows struct {
	c       *client
	ctx     context.Context
	stmt    string
	params  []types.AttributeValue
	limit   int
	token   *string
	buf     []map[string]types.AttributeValue
	emitted int
	cols    []string
	kinds   kinds
}

func (r *rows) fetch() error {
	in := &ddb.ExecuteStatementInput{Statement: aws.String(r.stmt), Parameters: r.params, NextToken: r.token}
	if r.limit > 0 {
		// Limit caps the items evaluated per request; paging continues with NextToken.
		in.Limit = aws.Int32(int32(min(r.limit-r.emitted-len(r.buf), 1000)))
		if *in.Limit <= 0 {
			r.token = nil
			return nil
		}
	}
	out, err := r.c.api.ExecuteStatement(r.ctx, in)
	if err != nil {
		return err
	}
	r.token = out.NextToken
	for _, it := range out.Items {
		for name, v := range it {
			r.kinds.add(name, kind(v))
		}
	}
	r.buf = append(r.buf, out.Items...)
	return nil
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { r.buf, r.token = nil, nil; return nil }
func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	if i < len(r.cols) {
		return r.kinds.typeOf(r.cols[i])
	}
	return ""
}

func (r *rows) Next(dest []sqldriver.Value) error {
	for len(r.buf) == 0 {
		if r.token == nil || (r.limit > 0 && r.emitted >= r.limit) {
			return io.EOF
		}
		if err := r.fetch(); err != nil {
			return err
		}
	}
	if r.limit > 0 && r.emitted >= r.limit {
		return io.EOF
	}
	item := r.buf[0]
	r.buf = r.buf[1:]
	r.emitted++
	for i := range dest {
		dest[i] = nil
		if i < len(r.cols) {
			if v, ok := item[r.cols[i]]; ok {
				dest[i] = toValue(v)
			}
		}
	}
	return nil
}

// staticRows serves results computed by the driver (COUNT).
type staticRows struct {
	cols []string
	vals [][]sqldriver.Value
	i    int
}

func (s *staticRows) Columns() []string { return s.cols }
func (s *staticRows) Close() error      { return nil }
func (s *staticRows) Next(dest []sqldriver.Value) error {
	if s.i >= len(s.vals) {
		return io.EOF
	}
	copy(dest, s.vals[s.i])
	s.i++
	return nil
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
			return fmt.Errorf("dynamodb: unexpected connection type %T", dc)
		}
		return fn(cn.c)
	})
}
