package dynamodb

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/mdenesfe/TableF/internal/driver"
)

// fake serves one table "users" with key id (N) and items u0..uN-1, two per page.
type fake struct {
	items    []map[string]types.AttributeValue
	stmts    []string
	params   [][]types.AttributeValue
	limits   []int32
	scans    int
	itemCnt  int64
	pageSize int
}

func newFake(n int) *fake {
	f := &fake{pageSize: 2, itemCnt: int64(n)}
	for i := range n {
		it := map[string]types.AttributeValue{
			"id":   &types.AttributeValueMemberN{Value: strconv.Itoa(i)},
			"name": &types.AttributeValueMemberS{Value: fmt.Sprintf("u%d", i)},
		}
		if i%2 == 1 {
			it["tags"] = &types.AttributeValueMemberSS{Value: []string{"a", "b"}}
		}
		f.items = append(f.items, it)
	}
	return f
}

func (f *fake) ExecuteStatement(_ context.Context, in *ddb.ExecuteStatementInput, _ ...func(*ddb.Options)) (*ddb.ExecuteStatementOutput, error) {
	f.stmts = append(f.stmts, aws.ToString(in.Statement))
	f.params = append(f.params, in.Parameters)
	if in.Limit != nil {
		f.limits = append(f.limits, *in.Limit)
	}
	start := 0
	if in.NextToken != nil {
		start, _ = strconv.Atoi(*in.NextToken)
	}
	size := f.pageSize
	if in.Limit != nil && int(*in.Limit) < size {
		size = int(*in.Limit)
	}
	end := min(start+size, len(f.items))
	out := &ddb.ExecuteStatementOutput{Items: f.items[start:end]}
	if end < len(f.items) {
		out.NextToken = aws.String(strconv.Itoa(end))
	}
	return out, nil
}

func (f *fake) ListTables(_ context.Context, in *ddb.ListTablesInput, _ ...func(*ddb.Options)) (*ddb.ListTablesOutput, error) {
	if in.ExclusiveStartTableName == nil {
		return &ddb.ListTablesOutput{TableNames: []string{"users"}, LastEvaluatedTableName: aws.String("users")}, nil
	}
	return &ddb.ListTablesOutput{TableNames: []string{"orders"}}, nil
}

func (f *fake) DescribeTable(_ context.Context, in *ddb.DescribeTableInput, _ ...func(*ddb.Options)) (*ddb.DescribeTableOutput, error) {
	return &ddb.DescribeTableOutput{Table: &types.TableDescription{
		TableName: in.TableName,
		ItemCount: aws.Int64(f.itemCnt),
		KeySchema: []types.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash}},
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeN},
		},
	}}, nil
}

func (f *fake) Scan(_ context.Context, in *ddb.ScanInput, _ ...func(*ddb.Options)) (*ddb.ScanOutput, error) {
	f.scans++
	start := 0
	if in.ExclusiveStartKey != nil {
		start, _ = strconv.Atoi(in.ExclusiveStartKey["id"].(*types.AttributeValueMemberN).Value)
	}
	end := min(start+f.pageSize, len(f.items))
	out := &ddb.ScanOutput{Count: int32(end - start)}
	if in.Select != types.SelectCount {
		out.Items = f.items[start:end]
	}
	if end < len(f.items) && (in.Limit == nil || int(*in.Limit) > end-start) {
		out.LastEvaluatedKey = map[string]types.AttributeValue{"id": &types.AttributeValueMemberN{Value: strconv.Itoa(end)}}
	}
	return out, nil
}

func open(f *fake) *sql.DB { return sql.OpenDB(connector{c: newClient(f, "eu-west-1")}) }

func TestRegistered(t *testing.T) {
	d, err := driver.Get("dynamodb")
	if err != nil {
		t.Fatal(err)
	}
	ft := driver.FeaturesOf(d)
	if ft.Family != "partiql" || ft.Schemas || ft.Groups || ft.Transactions || !ft.Edit {
		t.Fatalf("features %+v", ft)
	}
	if driver.QualifiedName(d, d.DefaultSchema(driver.Config{}), "users") != `"users"` {
		t.Fatal("qualified name")
	}
	if _, err := d.Open(driver.Config{}); err == nil {
		t.Fatal("expected region error")
	}
}

func TestStripLimit(t *testing.T) {
	q := withLimit(`SELECT * FROM "t" WHERE "a" = ?`, 350)
	got, n := stripLimit(q)
	if got != `SELECT * FROM "t" WHERE "a" = ?` || n != 350 {
		t.Fatalf("%q %d", got, n)
	}
	if got, n := stripLimit(`SELECT 1 -- note`); got != `SELECT 1 -- note` || n != 0 {
		t.Fatal(got, n)
	}
}

func TestCountShape(t *testing.T) {
	cases := []struct{ q, table, where string }{
		{`SELECT COUNT(*) FROM "users"`, `"users"`, ""},
		{`select count( * ) from users where "a" = ?`, "users", `"a" = ?`},
		{`SELECT COUNT(*) FROM "we""ird" WHERE ("x" > 1) AND "y" = ?`, `"we""ird"`, `("x" > 1) AND "y" = ?`},
	}
	for _, c := range cases {
		table, where, ok := countQuery(c.q)
		if !ok || table != c.table || where != c.where {
			t.Errorf("%s: %q %q %v", c.q, table, where, ok)
		}
	}
	if _, _, ok := countQuery(`SELECT * FROM "users"`); ok {
		t.Error("not a count")
	}
	if tableName(`"we""ird"`) != `we"ird` || tableName(`"t"."idx"`) != "t" || tableName("t.idx") != "t" {
		t.Error("tableName")
	}
}

func TestTranslateInsert(t *testing.T) {
	got, err := translateInsert(`INSERT INTO "users" ("id", "it's", "name") VALUES (?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "users" VALUE {'id': ?, 'it''s': ?, 'name': ?}`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	got, _ = translateInsert(`insert into t (a, b) values ('x, y', {'k': [1, 2]})`)
	if want := `INSERT INTO t VALUE {'a': 'x, y', 'b': {'k': [1, 2]}}`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	native := `INSERT INTO "users" VALUE {'id': 1}`
	if got, _ := translateInsert(native); got != native {
		t.Fatal(got)
	}
	if _, err := translateInsert(`INSERT INTO t (a, b) VALUES (?)`); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestProjection(t *testing.T) {
	if p := projection(`SELECT "b", a FROM "t"`); !reflect.DeepEqual(p, []string{"b", "a"}) {
		t.Fatal(p)
	}
	for _, q := range []string{`SELECT * FROM t`, `SELECT a.b FROM t`, `SELECT size(a) FROM t`} {
		if p := projection(q); p != nil {
			t.Errorf("%s: %v", q, p)
		}
	}
}

func TestToValue(t *testing.T) {
	cases := []struct {
		in   types.AttributeValue
		want any
	}{
		{&types.AttributeValueMemberS{Value: "x"}, "x"},
		{&types.AttributeValueMemberN{Value: "42"}, int64(42)},
		{&types.AttributeValueMemberN{Value: "0.1"}, 0.1},
		{&types.AttributeValueMemberN{Value: "12345678901234567890"}, "12345678901234567890"},
		{&types.AttributeValueMemberN{Value: "0.123456789012345678901"}, "0.123456789012345678901"},
		{&types.AttributeValueMemberBOOL{Value: true}, true},
		{&types.AttributeValueMemberNULL{Value: true}, nil},
		{&types.AttributeValueMemberB{Value: []byte("hi")}, "aGk="},
		{&types.AttributeValueMemberSS{Value: []string{"a", "b"}}, `["a","b"]`},
		{&types.AttributeValueMemberNS{Value: []string{"1", "2.5"}}, `[1,2.5]`},
		{&types.AttributeValueMemberL{Value: []types.AttributeValue{
			&types.AttributeValueMemberN{Value: "12345678901234567890"}, &types.AttributeValueMemberNULL{Value: true}}}, `[12345678901234567890,null]`},
		{&types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
			"a": &types.AttributeValueMemberS{Value: "x"}, "b": &types.AttributeValueMemberBOOL{}}}, `{"a":"x","b":false}`},
	}
	for _, c := range cases {
		if got := toValue(c.in); got != c.want {
			t.Errorf("%T: got %#v want %#v", c.in, got, c.want)
		}
	}
}

func TestToAttribute(t *testing.T) {
	cases := []struct {
		in   any
		want types.AttributeValue
	}{
		{nil, &types.AttributeValueMemberNULL{Value: true}},
		{"x", &types.AttributeValueMemberS{Value: "x"}},
		{float64(3), &types.AttributeValueMemberN{Value: "3"}},
		{1.25, &types.AttributeValueMemberN{Value: "1.25"}},
		{int64(7), &types.AttributeValueMemberN{Value: "7"}},
		{true, &types.AttributeValueMemberBOOL{Value: true}},
		{[]any{"a", 1.0}, &types.AttributeValueMemberL{Value: []types.AttributeValue{
			&types.AttributeValueMemberS{Value: "a"}, &types.AttributeValueMemberN{Value: "1"}}}},
		{map[string]any{"k": false}, &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
			"k": &types.AttributeValueMemberBOOL{Value: false}}}},
	}
	for _, c := range cases {
		got, err := toAttribute(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%#v: got %#v (%v)", c.in, got, err)
		}
	}
}

// TestBrowsePage drives the driver the way core.GetTableData does.
func TestBrowsePage(t *testing.T) {
	f := newFake(7)
	db := open(f)
	defer db.Close()
	e := engine{}
	q, skip := driver.Paginate(e, `SELECT * FROM "users"`, 3, 2)
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	cols, _ := rows.Columns()
	if !reflect.DeepEqual(cols, []string{"id", "name", "tags"}) {
		t.Fatalf("columns %v", cols)
	}
	types, _ := rows.ColumnTypes()
	if types[0].DatabaseTypeName() != "N" || types[2].DatabaseTypeName() != "SS" {
		t.Fatalf("types %s %s", types[0].DatabaseTypeName(), types[2].DatabaseTypeName())
	}
	var got [][]any
	for rows.Next() {
		v := make([]any, 3)
		if err := rows.Scan(&v[0], &v[1], &v[2]); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 { // limit+offset
		t.Fatalf("read %d rows", len(got))
	}
	page := got[skip:]
	if page[0][0] != int64(2) || page[2][0] != int64(4) {
		t.Fatalf("page %v", page)
	}
	if page[0][2] != nil || page[1][2] != `["a","b"]` {
		t.Fatalf("missing attribute should be NULL: %v", page)
	}
	if f.stmts[0] != `SELECT * FROM "users"` {
		t.Fatalf("marker not stripped: %q", f.stmts[0])
	}
	for _, l := range f.limits {
		if l > 5 {
			t.Fatalf("over-fetching limit %d", l)
		}
	}
}

func TestCount(t *testing.T) {
	f := newFake(5)
	db := open(f)
	defer db.Close()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM "users"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 || f.scans != 3 {
		t.Fatalf("count %d via %d scans", n, f.scans)
	}
	// Large tables use DescribeTable's ItemCount instead of scanning.
	f.itemCnt, f.scans = approxCountThreshold+1, 0
	if err := db.QueryRow(`SELECT COUNT(*) FROM "users"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != approxCountThreshold+1 || f.scans != 0 {
		t.Fatalf("approx count %d, scans %d", n, f.scans)
	}
	// With a condition the filtered SELECT is run, projecting the key only.
	f.stmts = nil
	if err := db.QueryRow(`SELECT COUNT(*) FROM "users" WHERE "name" = ?`, "u1").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 || f.stmts[0] != `SELECT "id" FROM "users" WHERE "name" = ?` {
		t.Fatalf("filtered count %d via %q", n, f.stmts)
	}
	if s, ok := f.params[len(f.params)-1][0].(*types.AttributeValueMemberS); !ok || s.Value != "u1" {
		t.Fatalf("params %#v", f.params)
	}
}

func TestExecInsert(t *testing.T) {
	f := newFake(0)
	db := open(f)
	defer db.Close()
	r, err := db.Exec(`INSERT INTO "users" ("id", "name") VALUES (?, ?)`, float64(9), "x")
	if err != nil {
		t.Fatal(err)
	}
	if f.stmts[0] != `INSERT INTO "users" VALUE {'id': ?, 'name': ?}` {
		t.Fatal(f.stmts[0])
	}
	if n, ok := f.params[0][0].(*types.AttributeValueMemberN); !ok || n.Value != "9" {
		t.Fatalf("params %#v", f.params[0])
	}
	if _, err := r.RowsAffected(); err == nil {
		t.Fatal("RowsAffected should report it is unknown")
	}
}

func TestCatalog(t *testing.T) {
	f := newFake(3)
	db := open(f)
	defer db.Close()
	ctx := context.Background()
	e := engine{}
	tables, err := e.Tables(ctx, db)
	if err != nil || len(tables) != 2 || tables[0].Name != "orders" || tables[1].Type != "table" {
		t.Fatalf("%v %v", tables, err)
	}
	cols, err := e.Columns(ctx, db, "", "users")
	if err != nil {
		t.Fatal(err)
	}
	want := []driver.Column{
		{Name: "id", Type: "N", PrimaryKey: true},
		{Name: "name", Type: "S", Nullable: true},
		{Name: "tags", Type: "SS", Nullable: true},
	}
	if !reflect.DeepEqual(cols, want) {
		t.Fatalf("%+v", cols)
	}
	if v := e.Version(ctx, db); v != "DynamoDB (eu-west-1)" {
		t.Fatal(v)
	}
	if dbs, _ := e.Databases(ctx, db); !reflect.DeepEqual(dbs, []string{"eu-west-1"}) {
		t.Fatal(dbs)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
}
