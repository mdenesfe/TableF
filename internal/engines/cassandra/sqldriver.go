package cassandra

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"

	"github.com/mdenesfe/TableF/internal/driver"
)

// DriverName is the database/sql driver name. Open DSNs are not supported:
// connections are made through sql.OpenDB with a connector built from a Config.
const DriverName = "tablef-cassandra"

func init() { sql.Register(DriverName, sqlDriver{}) }

type sqlDriver struct{}

func (sqlDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errors.New("tablef-cassandra: use sql.OpenDB with a connector")
}

// connector owns one gocql session (which is itself a pool and safe for
// concurrent use); every database/sql connection shares it.
type connector struct {
	cluster     *gocql.ClusterConfig
	mu          sync.Mutex
	session     *gocql.Session
	consistency gocql.Consistency
}

func splitHosts(host string, port int) []string {
	var out []string
	for _, h := range strings.Split(host, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(h); err != nil {
			h = net.JoinHostPort(strings.Trim(h, "[]"), strconv.Itoa(port))
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		out = []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	}
	return out
}

// parseConsistency defaults to LOCAL_QUORUM: reads see the GUI's own writes
// and it works on single-node development clusters as well.
func parseConsistency(s string) (gocql.Consistency, error) {
	if strings.TrimSpace(s) == "" {
		return gocql.LocalQuorum, nil
	}
	return gocql.ParseConsistencyWrapper(strings.ToUpper(strings.TrimSpace(s)))
}

func newConnector(cfg driver.Config) (*connector, error) {
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	cons, err := parseConsistency(cfg.Opt("consistency", ""))
	if err != nil {
		return nil, fmt.Errorf("cassandra: invalid consistency: %w", err)
	}
	cl := gocql.NewCluster(splitHosts(cfg.Host, port)...)
	cl.Keyspace = cfg.Database
	cl.Consistency = cons
	cl.Timeout = 30 * time.Second
	cl.ConnectTimeout = 10 * time.Second
	cl.NumConns = 1
	if cfg.User != "" {
		cl.Authenticator = gocql.PasswordAuthenticator{Username: cfg.User, Password: cfg.Password}
	}
	if dc := cfg.Opt("datacenter", ""); dc != "" {
		cl.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(gocql.DCAwareRoundRobinPolicy(dc))
	}
	if cfg.SSHEnabled {
		// Through a tunnel only the forwarded node is reachable.
		cl.DisableInitialHostLookup = true
	}
	switch strings.ToLower(cfg.SSLMode) {
	case "", "disable", "disabled", "false", "off":
	case "verify-full", "verify-ca":
		cl.SslOpts = &gocql.SslOptions{EnableHostVerification: true, CaPath: cfg.Opt("caFile", "")}
	default:
		cl.SslOpts = &gocql.SslOptions{EnableHostVerification: false, CaPath: cfg.Opt("caFile", "")}
	}
	return &connector{cluster: cl, consistency: cons}, nil
}

func (c *connector) getSession() (*gocql.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil && !c.session.Closed() {
		return c.session, nil
	}
	s, err := c.cluster.CreateSession()
	if err != nil {
		return nil, err
	}
	c.session = s
	return s, nil
}

func (c *connector) Connect(context.Context) (sqldriver.Conn, error) {
	s, err := c.getSession()
	if err != nil {
		return nil, err
	}
	return &conn{s: s, consistency: c.consistency}, nil
}

func (c *connector) Driver() sqldriver.Driver { return sqlDriver{} }

// Close is called by sql.DB.Close.
func (c *connector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}
	return nil
}

var _ io.Closer = (*connector)(nil)

type conn struct {
	s           *gocql.Session
	consistency gocql.Consistency
}

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
func (c *conn) Close() error { return nil } // the session belongs to the connector
func (c *conn) Begin() (sqldriver.Tx, error) {
	return nil, errors.New("cassandra: transactions are not supported")
}

// CheckNamedValue accepts any Go value; flexArg converts it per CQL type.
func (c *conn) CheckNamedValue(*sqldriver.NamedValue) error { return nil }

func (c *conn) Ping(ctx context.Context) error {
	if c.s.Closed() {
		return sqldriver.ErrBadConn
	}
	return c.s.Query("SELECT release_version FROM system.local").WithContext(ctx).Exec()
}

func bindArgs(args []sqldriver.NamedValue) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = flexArg{a.Value}
	}
	return out
}

func (c *conn) query(ctx context.Context, q string, args []sqldriver.NamedValue) *gocql.Query {
	return c.s.Query(strings.TrimSpace(q), bindArgs(args)...).WithContext(ctx).Consistency(c.consistency)
}

func (c *conn) ExecContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	if err := c.query(ctx, q, args).Exec(); err != nil {
		return nil, err
	}
	return result{}, nil
}

func (c *conn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	iter := c.query(ctx, q, args).PageSize(1000).Iter()
	return newRows(iter)
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

// result: Cassandra does not report affected rows for writes (they are
// upserts/tombstones). RowsAffected returns an error so callers that check
// "exactly one row changed" skip the check instead of failing.
type result struct{}

func (result) LastInsertId() (int64, error) {
	return 0, errors.New("cassandra: no insert ids")
}
func (result) RowsAffected() (int64, error) {
	return 0, errors.New("cassandra: affected rows are not reported")
}

type rows struct {
	iter  *gocql.Iter
	cols  []string
	types []string
	zero  []reflect.Type // element type per output column, nil => any
}

func newRows(iter *gocql.Iter) (*rows, error) {
	rd, err := iter.RowData()
	if err != nil {
		iter.Close()
		return nil, err
	}
	r := &rows{iter: iter, cols: rd.Columns}
	for _, ci := range iter.Columns() {
		if tt, ok := ci.TypeInfo.(gocql.TupleTypeInfo); ok {
			for _, e := range tt.Elems {
				r.types = append(r.types, typeName(e))
			}
			continue
		}
		r.types = append(r.types, typeName(ci.TypeInfo))
	}
	for _, v := range rd.Values {
		r.zero = append(r.zero, reflect.TypeOf(v).Elem())
	}
	return r, nil
}

var nativeNames = map[gocql.Type]string{
	gocql.TypeAscii: "ascii", gocql.TypeBigInt: "bigint", gocql.TypeBlob: "blob",
	gocql.TypeBoolean: "boolean", gocql.TypeCounter: "counter", gocql.TypeDecimal: "decimal",
	gocql.TypeDouble: "double", gocql.TypeFloat: "float", gocql.TypeInt: "int",
	gocql.TypeText: "text", gocql.TypeTimestamp: "timestamp", gocql.TypeUUID: "uuid",
	gocql.TypeVarchar: "varchar", gocql.TypeVarint: "varint", gocql.TypeTimeUUID: "timeuuid",
	gocql.TypeInet: "inet", gocql.TypeDate: "date", gocql.TypeTime: "time",
	gocql.TypeSmallInt: "smallint", gocql.TypeTinyInt: "tinyint", gocql.TypeDuration: "duration",
	gocql.TypeList: "list", gocql.TypeSet: "set", gocql.TypeMap: "map",
	gocql.TypeUDT: "udt", gocql.TypeTuple: "tuple", gocql.TypeCustom: "custom",
}

// typeName renders a CQL type like cqlsh does, e.g. map<text, int>.
func typeName(t gocql.TypeInfo) string {
	if t == nil {
		return ""
	}
	switch v := t.(type) {
	case gocql.CollectionType:
		if v.Type() == gocql.TypeMap {
			return "map<" + typeName(v.Key) + ", " + typeName(v.Elem) + ">"
		}
		return nativeNames[v.Type()] + "<" + typeName(v.Elem) + ">"
	case gocql.UDTTypeInfo:
		return v.Name
	case gocql.TupleTypeInfo:
		parts := make([]string, len(v.Elems))
		for i, e := range v.Elems {
			parts[i] = typeName(e)
		}
		return "tuple<" + strings.Join(parts, ", ") + ">"
	}
	if n, ok := nativeNames[t.Type()]; ok {
		return n
	}
	return fmt.Sprint(t.Type())
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { return r.iter.Close() }
func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	if i < len(r.types) {
		return r.types[i]
	}
	return ""
}

func (r *rows) Next(dest []sqldriver.Value) error {
	// Scan into **T so CQL nulls come back as nil pointers instead of zero values.
	ptrs := make([]any, len(r.zero))
	for i, t := range r.zero {
		ptrs[i] = reflect.New(reflect.PointerTo(t)).Interface()
	}
	if !r.iter.Scan(ptrs...) {
		if err := r.iter.Close(); err != nil {
			return err
		}
		return io.EOF
	}
	for i := range dest {
		if i >= len(ptrs) {
			dest[i] = nil
			continue
		}
		p := reflect.ValueOf(ptrs[i]).Elem() // *T
		if p.IsNil() {
			dest[i] = nil
			continue
		}
		dest[i] = toJSONValue(p.Elem().Interface())
	}
	return nil
}
