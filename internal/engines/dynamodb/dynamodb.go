// Package dynamodb adds Amazon DynamoDB as a SQL engine speaking PartiQL. A
// thin database/sql driver ("tablef-dynamodb") runs statements through the
// ExecuteStatement API and bridges what the core's generated SQL needs (paging,
// COUNT(*), INSERT ... VALUES) — see translate.go.
//
// Options: region (required), accessKeyId, secretAccessKey, sessionToken
// (empty = the default AWS credential chain), endpoint (e.g.
// http://localhost:8000 for DynamoDB Local).
package dynamodb

import (
	"context"
	"database/sql"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/mdenesfe/TableF/internal/driver"
)

type engine struct {
	driver.NoForeignKeys
	driver.NoRoutines
}

func init() { driver.Register(engine{}) }

func (engine) Name() string { return "dynamodb" }

// Features: tables live directly in a region (no schemas); PartiQL has no
// GROUP BY, foreign keys or routines, and ExecuteStatement runs one statement
// at a time (ExecuteTransaction is not exposed through database/sql here).
// Single-item UPDATE/DELETE by key and INSERT work, so editing is on.
func (engine) Features() driver.Features {
	return driver.Features{Family: "partiql", Edit: true}
}

func (engine) Open(cfg driver.Config) (*sql.DB, error) {
	c, err := buildClient(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector{c: c}), nil
}

func (engine) QuoteIdent(name string) string      { return driver.QuoteWith(name, `"`) }
func (engine) Placeholder(int) string             { return "?" }
func (engine) DefaultSchema(driver.Config) string { return "" }

// Paginate: PartiQL has no LIMIT/OFFSET. The query is tagged with a trailing
// "-- tablef:limit N" comment (N = limit+offset) that the driver strips; it
// stops reading after N items and the core drops the first offset rows.
func (engine) Paginate(q string, limit, offset int) (string, int) {
	return withLimit(q, limit+offset), offset
}

// EmptyInsert: an item needs its key attributes, so a row of defaults cannot
// exist; the statement fails with a clear server error.
func (engine) EmptyInsert(table string) string { return "INSERT INTO " + table + " VALUE {}" }

func (engine) Version(ctx context.Context, db *sql.DB) string {
	v := "DynamoDB"
	_ = withClient(ctx, db, func(c *client) error {
		v += " (" + c.region + ")"
		return nil
	})
	return v
}

func (engine) CurrentDatabase(ctx context.Context, db *sql.DB) string {
	var r string
	_ = withClient(ctx, db, func(c *client) error { r = c.region; return nil })
	return r
}

func (e engine) Databases(ctx context.Context, db *sql.DB) ([]string, error) {
	var out []string
	err := withClient(ctx, db, func(c *client) error { out = []string{c.region}; return nil })
	return out, err
}

func (engine) Schemas(context.Context, *sql.DB) ([]string, error) { return []string{}, nil }

func (engine) Tables(ctx context.Context, db *sql.DB) ([]driver.Table, error) {
	out := []driver.Table{}
	err := withClient(ctx, db, func(c *client) error {
		var start *string
		for {
			res, err := c.api.ListTables(ctx, &ddb.ListTablesInput{ExclusiveStartTableName: start})
			if err != nil {
				return err
			}
			for _, n := range res.TableNames {
				out = append(out, driver.Table{Name: n, Type: "table"})
			}
			if res.LastEvaluatedTableName == nil {
				return nil
			}
			start = res.LastEvaluatedTableName
		}
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// sampleSize is how many items Columns scans to infer non-key attributes.
const sampleSize = 100

func (engine) Columns(ctx context.Context, db *sql.DB, _, table string) ([]driver.Column, error) {
	var cols []driver.Column
	err := withClient(ctx, db, func(c *client) error {
		keys, keyTyp, err := c.keySchema(ctx, table)
		if err != nil {
			return err
		}
		s, err := c.api.Scan(ctx, &ddb.ScanInput{TableName: aws.String(table), Limit: aws.Int32(sampleSize)})
		if err != nil {
			return err
		}
		cols = inferColumns(keys, keyTyp, s.Items)
		return nil
	})
	if cols == nil {
		cols = []driver.Column{}
	}
	return cols, err
}

// inferColumns lists key attributes first (primary key, not nullable), then
// every attribute seen in the sample with the DynamoDB type letters observed.
func inferColumns(keys []string, keyTyp map[string]string, items []map[string]types.AttributeValue) []driver.Column {
	k := kinds{}
	for name, t := range keyTyp {
		k.add(name, t)
	}
	for _, it := range items {
		for name, v := range it {
			k.add(name, kind(v))
		}
	}
	isKey := map[string]bool{}
	for _, n := range keys {
		isKey[n] = true
	}
	out := []driver.Column{}
	for _, n := range columnSet(keys, items) {
		out = append(out, driver.Column{Name: n, Type: k.typeOf(n), Nullable: !isKey[n], PrimaryKey: isKey[n]})
	}
	return out
}
