// Package mongodb browses MongoDB through the driver.Store interface.
package mongodb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"

	"github.com/mdenesfe/TableF/internal/driver"
)

func init() { driver.RegisterStore(store{}) }

// sampleSize is how many documents Columns looks at to infer fields.
const sampleSize = 100

// sampleTTL is how long a collection's sampled fields are reused while
// paging, so a page load doesn't re-read the sample every time.
const sampleTTL = 30 * time.Second

// closeTimeout bounds Disconnect, which waits for in-flight operations.
const closeTimeout = 5 * time.Second

// maxShellRows caps documents read by one editor command (the core keeps 5000).
const maxShellRows = 5001

type store struct{}

func (store) Name() string { return "mongodb" }

// buildURI returns the connection string and the database to use.
// Options: uri (wins when set), authSource, tls ("true"), tlsInsecure, replicaSet,
// directConnection ("true").
func buildURI(cfg driver.Config) (string, error) {
	if u := strings.TrimSpace(cfg.Opt("uri", "")); u != "" {
		return u, nil
	}
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 27017
	}
	u := url.URL{Scheme: "mongodb", Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/"}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	q := url.Values{}
	if cfg.User != "" {
		q.Set("authSource", cfg.Opt("authSource", "admin"))
	} else if s := cfg.Opt("authSource", ""); s != "" {
		q.Set("authSource", s)
	}
	if cfg.Opt("tls", "") == "true" {
		q.Set("tls", "true")
	}
	if cfg.Opt("tlsInsecure", "") == "true" {
		q.Set("tlsInsecure", "true")
	}
	if rs := cfg.Opt("replicaSet", ""); rs != "" {
		q.Set("replicaSet", rs)
	}
	if cfg.Opt("directConnection", "") == "true" {
		q.Set("directConnection", "true")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (store) Open(ctx context.Context, cfg driver.Config) (driver.StoreConn, error) {
	uri, err := buildURI(cfg)
	if err != nil {
		return nil, err
	}
	dbName := cfg.Database
	if dbName == "" {
		if cs, err := connstring.Parse(uri); err == nil {
			dbName = cs.Database
		}
	}
	if dbName == "" {
		dbName = "test"
	}
	opts := options.Client().ApplyURI(uri).
		SetServerSelectionTimeout(10 * time.Second).
		SetAppName("TableF")
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, cleanErr(err, cfg.Password)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, cleanErr(err, cfg.Password)
	}
	return &conn{client: client, db: client.Database(dbName), dbName: dbName}, nil
}

// cleanErr makes sure a connection error never echoes the password.
func cleanErr(err error, password string) error {
	if password == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, password) && !strings.Contains(msg, url.QueryEscape(password)) {
		return err
	}
	msg = strings.ReplaceAll(msg, password, "****")
	msg = strings.ReplaceAll(msg, url.QueryEscape(password), "****")
	return errors.New(msg)
}

type conn struct {
	client *mongo.Client
	db     *mongo.Database
	dbName string

	mu      sync.Mutex
	samples map[string]sampled // by collection
}

// sampled is the field set inferred from a collection's sample.
type sampled struct {
	fs *fieldSet
	at time.Time
}

// Close disconnects, giving in-flight operations closeTimeout to finish.
func (c *conn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return c.client.Disconnect(ctx)
}

func (c *conn) Features() driver.Features {
	return driver.Features{Family: "mongodb", Edit: true}
}

func (c *conn) Version(ctx context.Context) string {
	var info struct {
		Version string `bson:"version"`
	}
	if err := c.client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info); err != nil || info.Version == "" {
		return "MongoDB"
	}
	return "MongoDB " + info.Version
}

func (c *conn) Databases(ctx context.Context) ([]string, error) {
	names, err := c.client.ListDatabaseNames(ctx, bson.D{}, options.ListDatabases().SetAuthorizedDatabases(true))
	if err != nil {
		// Users without listDatabases still get the database they connected to.
		return []string{c.dbName}, nil
	}
	found := false
	for _, n := range names {
		if n == c.dbName {
			found = true
		}
	}
	if !found {
		names = append(names, c.dbName)
	}
	sort.Strings(names)
	return names, nil
}

func (c *conn) Tables(ctx context.Context) ([]driver.Table, error) {
	specs, err := c.db.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	out := make([]driver.Table, 0, len(specs))
	for _, s := range specs {
		if strings.HasPrefix(s.Name, "system.") {
			continue
		}
		t := "table"
		if s.Type == "view" {
			t = "view"
		}
		out = append(out, driver.Table{Name: s.Name, Type: t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// sample reads up to sampleSize documents of a collection.
func (c *conn) sample(ctx context.Context, table string) ([]bson.D, error) {
	cur, err := c.db.Collection(table).Find(ctx, bson.D{}, options.Find().SetLimit(sampleSize))
	if err != nil {
		return nil, err
	}
	var docs []bson.D
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// sampleFields returns the fields of a collection's sample, cached for
// sampleTTL. The result is the caller's to extend.
func (c *conn) sampleFields(ctx context.Context, table string) (*fieldSet, error) {
	c.mu.Lock()
	s, ok := c.samples[table]
	c.mu.Unlock()
	if ok && time.Since(s.at) < sampleTTL {
		return s.fs.clone(), nil
	}
	docs, err := c.sample(ctx, table)
	if err != nil {
		return nil, err
	}
	fs := newFieldSet()
	for _, d := range docs {
		fs.add(d)
	}
	c.mu.Lock()
	if c.samples == nil {
		c.samples = map[string]sampled{}
	}
	c.samples[table] = sampled{fs: fs, at: time.Now()}
	c.mu.Unlock()
	return fs.clone(), nil
}

// forgetSamples drops cached field sets after writes.
func (c *conn) forgetSamples() {
	c.mu.Lock()
	c.samples = nil
	c.mu.Unlock()
}

func (c *conn) Columns(ctx context.Context, table string) ([]driver.Column, error) {
	fs, err := c.sampleFields(ctx, table)
	if err != nil {
		return nil, err
	}
	return fs.columns(), nil
}

// rowsFilter combines the typed filter with the exact column matches.
func rowsFilter(filter string, match []driver.Match) (bson.D, error) {
	f, err := parseDoc(filter)
	if err != nil {
		return nil, fmt.Errorf("filter: %w", err)
	}
	if len(match) == 0 {
		return f, nil
	}
	conds := bson.A{}
	if len(f) > 0 {
		conds = append(conds, f)
	}
	for _, m := range match {
		conds = append(conds, bson.D{{Key: m.Column, Value: matchValue(m.Column, m.Value)}})
	}
	if len(conds) == 1 {
		return conds[0].(bson.D), nil
	}
	return bson.D{{Key: "$and", Value: conds}}, nil
}

// matchValue turns a grid value back into what is stored: hex strings into
// ObjectIDs for _id, and whole floats into integers ({n: 3} matches 3.0 anyway).
func matchValue(col string, v any) any {
	switch t := v.(type) {
	case nil:
		return nil // {f: null} matches null and missing
	case string:
		if col == "_id" {
			if oid, err := bson.ObjectIDFromHex(t); err == nil {
				return oid
			}
		}
		return t
	}
	return v
}

func (c *conn) Rows(ctx context.Context, req driver.RowsRequest) (driver.RowsResult, error) {
	filter, err := rowsFilter(req.Filter, req.Match)
	if err != nil {
		return driver.RowsResult{}, err
	}
	coll := c.db.Collection(req.Table)
	fo := options.Find()
	var sortDoc bson.D
	if req.OrderBy != "" {
		dir := 1
		if strings.EqualFold(req.OrderDir, "desc") {
			dir = -1
		}
		sortDoc = bson.D{{Key: req.OrderBy, Value: dir}}
		fo.SetSort(sortDoc)
	}
	if req.Offset > 0 {
		fo.SetSkip(int64(req.Offset))
	}
	if req.Limit > 0 {
		fo.SetLimit(int64(req.Limit))
	}
	q := collRef(req.Table) + ".find(" + shell(filter) + ")"
	if sortDoc != nil {
		q += ".sort(" + shell(sortDoc) + ")"
	}
	q += fmt.Sprintf(".skip(%d)", req.Offset)
	if req.Limit > 0 {
		q += fmt.Sprintf(".limit(%d)", req.Limit)
	}

	// The total and the sampled columns don't depend on the page; fetch them
	// while the page is read.
	var (
		total     int64
		countErr  error
		fs        *fieldSet
		sampleErr error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if len(filter) == 0 {
			total, countErr = coll.EstimatedDocumentCount(ctx)
		} else {
			total, countErr = coll.CountDocuments(ctx, filter)
		}
	}()
	go func() {
		defer wg.Done()
		fs, sampleErr = c.sampleFields(ctx, req.Table)
	}()
	var docs []bson.D
	cur, err := coll.Find(ctx, filter, fo)
	if err == nil {
		err = cur.All(ctx, &docs)
	}
	wg.Wait()
	if err != nil {
		return driver.RowsResult{}, err
	}
	if countErr != nil {
		total = int64(req.Offset + len(docs))
	}

	// Columns come from a sample of the collection plus everything on this page,
	// so they stay stable while paging and never hide a field.
	if sampleErr != nil {
		fs = newFieldSet()
	}
	for _, d := range docs {
		fs.add(d)
	}
	cols := fs.columns()
	names := make([]string, len(cols))
	for i, col := range cols {
		names[i] = col.Name
	}
	return driver.RowsResult{Columns: cols, Rows: alignRows(names, docs), Total: total, Query: q}, nil
}
