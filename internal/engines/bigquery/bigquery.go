// Package bigquery adds Google BigQuery as a SQL engine speaking GoogleSQL. A
// small database/sql driver ("tablef-bigquery") runs statements as query jobs
// through cloud.google.com/go/bigquery.
//
// Options: projectId (required), credentialsFile (path to a credentials JSON,
// e.g. a service-account key) or credentialsJSON (the JSON inline) — both
// empty means Application Default Credentials —, location (job location, e.g.
// "EU") and maxBytesBilled (optional per-query billing cap). cfg.Database is
// the default dataset.
package bigquery

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync"

	"cloud.google.com/go/bigquery"
	bq "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/iterator"

	"github.com/mdenesfe/TableF/internal/driver"
)

type engine struct {
	driver.NoForeignKeys
	driver.NoRoutines
}

func init() { driver.Register(engine{}) }

func (engine) Name() string { return "bigquery" }

// Features: datasets are the schemas. DML (UPDATE/DELETE/INSERT) works and
// reports affected rows, so editing is on; GROUP BY and the grouping queries'
// CASE/subquery shapes are valid GoogleSQL. Multi-statement transactions only
// exist inside scripts/sessions, which this driver does not keep, and
// BigQuery has no enforced foreign keys. Routines are not browsed.
func (engine) Features() driver.Features {
	return driver.Features{Family: "bigquery", Schemas: true, Groups: true, Edit: true}
}

func (engine) Open(cfg driver.Config) (*sql.DB, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector{c: c}), nil
}

// QuoteIdent quotes one identifier with backticks. GoogleSQL escapes a
// backtick inside a quoted identifier with a backslash.
func (engine) QuoteIdent(name string) string {
	r := strings.NewReplacer(`\`, `\\`, "`", "\\`")
	return "`" + r.Replace(name) + "`"
}

func (engine) Placeholder(int) string { return "?" }

func (engine) DefaultSchema(cfg driver.Config) string { return defaultDataset(cfg) }

// LIMIT n OFFSET m is valid GoogleSQL, so the core's default paging is used.
//
// Cost: every page is a query job billed by the bytes it scans, and LIMIT
// does not reduce what a SELECT * scans; set maxBytesBilled to cap it. The
// unfiltered row total is read from table metadata for free (see
// tableCount), while filtered totals and groups are billed queries too.
// Abandoned queries are canceled server-side (see cancelJob).

func (engine) Version(context.Context, *sql.DB) string { return "BigQuery" }

func (engine) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	var p string
	_ = withClient(ctx, db, func(c *client) error { p = c.project; return nil })
	return p
}

// Databases returns the project; datasets are listed as schemas.
func (engine) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	var out []string
	err := withClient(ctx, db, func(c *client) error { out = []string{c.project}; return nil })
	return out, err
}

func datasets(ctx context.Context, c *client) ([]string, error) {
	out := []string{}
	it := c.bq.Datasets(ctx)
	for {
		ds, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ds.DatasetID)
	}
	sort.Strings(out)
	return out, nil
}

func (engine) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	var out []string
	err := withClient(ctx, db, func(c *client) error {
		var err error
		out, err = datasets(ctx, c)
		return err
	})
	return out, err
}

// tableType maps the REST API's table type: VIEW and MATERIALIZED_VIEW are
// views; TABLE, EXTERNAL and SNAPSHOT are tables.
func tableType(t string) string {
	if strings.Contains(strings.ToUpper(t), "VIEW") {
		return "view"
	}
	return "table"
}

func (engine) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	out := []driver.Table{}
	err := withClient(ctx, db, func(c *client) error {
		dss, err := datasets(ctx, c)
		if err != nil {
			return err
		}
		// tables.list per dataset, a few at a time.
		var mu sync.Mutex
		var firstErr error
		sem := make(chan struct{}, 8)
		var wg sync.WaitGroup
		for _, ds := range dss {
			wg.Add(1)
			sem <- struct{}{}
			go func(ds string) {
				defer wg.Done()
				defer func() { <-sem }()
				var list []driver.Table
				err := c.raw.Tables.List(c.project, ds).MaxResults(1000).Pages(ctx, func(p *bq.TableList) error {
					for _, t := range p.Tables {
						if t.TableReference != nil {
							list = append(list, driver.Table{Schema: ds, Name: t.TableReference.TableId, Type: tableType(t.Type)})
						}
					}
					return nil
				})
				mu.Lock()
				defer mu.Unlock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				out = append(out, list...)
			}(ds)
		}
		wg.Wait()
		return firstErr
	})
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		return out[i].Name < out[j].Name
	})
	return out, err
}

func (engine) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	cols := []driver.Column{}
	err := withClient(ctx, db, func(c *client) error {
		if schema == "" {
			schema = c.dataset
		}
		if schema == "" {
			return errors.New("bigquery: no dataset given and no default dataset configured")
		}
		md, err := c.bq.Dataset(schema).Table(table).Metadata(ctx)
		if err != nil {
			return err
		}
		cols = columnsOf(md)
		return nil
	})
	return cols, err
}

// columnsOf lists top-level fields; a RECORD is one STRUCT column (its nested
// fields are shown as JSON in the data grid). Primary keys come from the
// table's (unenforced) PRIMARY KEY constraint, when declared.
func columnsOf(md *bigquery.TableMetadata) []driver.Column {
	pk := map[string]bool{}
	if md.TableConstraints != nil && md.TableConstraints.PrimaryKey != nil {
		for _, c := range md.TableConstraints.PrimaryKey.Columns {
			pk[c] = true
		}
	}
	out := []driver.Column{}
	for _, f := range md.Schema {
		col := driver.Column{
			Name:       f.Name,
			Type:       typeName(f),
			Nullable:   !f.Required && !f.Repeated,
			PrimaryKey: pk[f.Name],
		}
		if f.DefaultValueExpression != "" {
			d := f.DefaultValueExpression
			col.Default = &d
		}
		out = append(out, col)
	}
	return out
}
