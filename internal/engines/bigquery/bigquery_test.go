package bigquery

import (
	sqldriver "database/sql/driver"
	"math/big"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"

	"github.com/mdenesfe/TableF/internal/driver"
)

func TestRegistered(t *testing.T) {
	d, err := driver.Get("bigquery")
	if err != nil {
		t.Fatal(err)
	}
	f := driver.FeaturesOf(d)
	if f.Family != "bigquery" || !f.Schemas || !f.Groups || !f.Edit || f.Transactions || f.ForeignKeys {
		t.Fatalf("%+v", f)
	}
	if q := driver.QualifiedName(d, "ds", "my`t"); q != "`ds`.`my\\`t`" {
		t.Fatal(q)
	}
	if q, skip := driver.Paginate(d, "SELECT * FROM `t`", 10, 20); q != "SELECT * FROM `t` LIMIT 10 OFFSET 20" || skip != 0 {
		t.Fatal(q, skip)
	}
	if _, err := d.Open(driver.Config{}); err == nil {
		t.Fatal("expected projectId error")
	}
	if _, err := d.Open(driver.Config{Options: map[string]string{"projectId": "p", "credentialsJSON": `{"type":"nope"}`}}); err == nil {
		t.Fatal("expected credentials type error")
	}
}

func TestDefaultDataset(t *testing.T) {
	opt := map[string]string{"projectId": "my-proj"}
	if ds := defaultDataset(driver.Config{Database: "sales", Options: opt}); ds != "sales" {
		t.Fatal(ds)
	}
	if ds := defaultDataset(driver.Config{Database: "my-proj", Options: opt}); ds != "" {
		t.Fatal(ds)
	}
}

func TestPlaceholders(t *testing.T) {
	q := "SELECT '?', \"a?\", `c?`, r'\\?', '''x ? y''', ? -- ?\n# ?\n/* ? */ FROM t WHERE a = ? AND b = 'it\\'s?'"
	pos := placeholders(q)
	if len(pos) != 2 {
		t.Fatalf("found %d placeholders at %v", len(pos), pos)
	}
	for _, p := range pos {
		if q[p] != '?' {
			t.Fatal("bad offset")
		}
	}
}

func TestBind(t *testing.T) {
	q, params, err := bind("UPDATE `t` SET `a` = ?, `b` = ?, `c` = ? WHERE `k` = ?", []any{nil, "x?", 2.0, 1.5})
	if err != nil {
		t.Fatal(err)
	}
	if q != "UPDATE `t` SET `a` = NULL, `b` = ?, `c` = ? WHERE `k` = ?" {
		t.Fatal(q)
	}
	if len(params) != 3 || params[0].Name != "" || params[0].Value != "x?" || params[1].Value != int64(2) {
		t.Fatalf("%#v", params)
	}
	if r, ok := params[2].Value.(*big.Rat); !ok || r.FloatString(1) != "1.5" {
		t.Fatalf("%#v", params[2].Value)
	}
	if _, _, err := bind("SELECT ?", []any{1, 2}); err == nil {
		t.Fatal("expected count mismatch")
	}
	if q, p, _ := bind("SELECT 1", nil); q != "SELECT 1" || p != nil {
		t.Fatal(q, p)
	}
}

func TestParamValue(t *testing.T) {
	cases := []struct {
		in   any
		want any
	}{
		{float64(7), int64(7)},
		{1e300, 1e300},
		{0.1234567890123, 0.1234567890123}, // too many decimals for NUMERIC
		{"s", "s"},
		{true, true},
		{map[string]any{"a": 1.0}, `{"a":1}`},
		{[]any{"x"}, `["x"]`},
	}
	for _, c := range cases {
		got, null := paramValue(c.in)
		if null || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%#v: got %#v", c.in, got)
		}
	}
	if _, null := paramValue(nil); !null {
		t.Error("nil should be NULL")
	}
}

func TestCell(t *testing.T) {
	ts := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	num := &bigquery.FieldSchema{Name: "n", Type: bigquery.NumericFieldType}
	bignum := &bigquery.FieldSchema{Name: "b", Type: bigquery.BigNumericFieldType}
	rec := &bigquery.FieldSchema{Name: "r", Type: bigquery.RecordFieldType, Schema: bigquery.Schema{
		{Name: "x", Type: bigquery.IntegerFieldType},
		{Name: "tags", Type: bigquery.StringFieldType, Repeated: true},
	}}
	recs := &bigquery.FieldSchema{Name: "rs", Type: bigquery.RecordFieldType, Repeated: true, Schema: rec.Schema}
	arr := &bigquery.FieldSchema{Name: "a", Type: bigquery.IntegerFieldType, Repeated: true}
	cases := []struct {
		v    bigquery.Value
		f    *bigquery.FieldSchema
		want any
	}{
		{nil, nil, nil},
		{int64(5), nil, int64(5)},
		{"s", nil, "s"},
		{ts, nil, ts},
		{civil.Date{Year: 2024, Month: 1, Day: 2}, nil, "2024-01-02"},
		{civil.Time{Hour: 13, Minute: 4, Second: 5, Nanosecond: 600000000}, nil, "13:04:05.600000"},
		{civil.DateTime{Date: civil.Date{Year: 2024, Month: 1, Day: 2}, Time: civil.Time{Hour: 3}}, nil, "2024-01-02 03:00:00"},
		{big.NewRat(5, 2), num, "2.5"},
		{big.NewRat(-1, 3), bignum, "-0.33333333333333333333333333333333333333"},
		{big.NewRat(10, 1), num, "10"},
		{[]byte("hi"), nil, "aGk="},
		{[]bigquery.Value{int64(1), int64(2)}, arr, "[1,2]"},
		{[]bigquery.Value{int64(1), []bigquery.Value{"a", "b"}}, rec, `{"tags":["a","b"],"x":1}`},
		{[]bigquery.Value{[]bigquery.Value{int64(1), nil}}, recs, `[{"tags":null,"x":1}]`},
		{[]bigquery.Value{int64(1) << 60, ts}, nil, `["1152921504606846976","2024-05-06T07:08:09Z"]`},
	}
	for _, c := range cases {
		if got := cell(c.v, c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%#v: got %#v want %#v", c.v, got, c.want)
		}
	}
}

func TestColumnsOf(t *testing.T) {
	def := "CURRENT_TIMESTAMP()"
	md := &bigquery.TableMetadata{
		Schema: bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType, Required: true},
			{Name: "tags", Type: bigquery.StringFieldType, Repeated: true},
			{Name: "addr", Type: bigquery.RecordFieldType, Schema: bigquery.Schema{{Name: "city", Type: bigquery.StringFieldType}}},
			{Name: "at", Type: bigquery.TimestampFieldType, DefaultValueExpression: def},
		},
		TableConstraints: &bigquery.TableConstraints{PrimaryKey: &bigquery.PrimaryKey{Columns: []string{"id"}}},
	}
	got := columnsOf(md)
	want := []driver.Column{
		{Name: "id", Type: "INT64", PrimaryKey: true},
		{Name: "tags", Type: "ARRAY<STRING>"},
		{Name: "addr", Type: "STRUCT", Nullable: true},
		{Name: "at", Type: "TIMESTAMP", Nullable: true, Default: &def},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if tableType("MATERIALIZED_VIEW") != "view" || tableType("EXTERNAL") != "table" {
		t.Fatal("tableType")
	}
}

func TestFillRow(t *testing.T) {
	d := make([]sqldriver.Value, 3)
	fillRow(d, []bigquery.Value{int64(1), "x"}, bigquery.Schema{{Name: "a"}, {Name: "b"}, {Name: "c"}})
	if d[0] != int64(1) || d[1] != "x" || d[2] != nil {
		t.Fatal(d)
	}
}

func TestCountAllPattern(t *testing.T) {
	d := engine{}
	for _, c := range []struct{ ds, table string }{{"sales", "orders"}, {"my`ds", `t\x`}, {"", "solo"}} {
		q := "SELECT COUNT(*) FROM " + driver.QualifiedName(d, c.ds, c.table)
		m := countAll.FindStringSubmatch(q)
		if m == nil || unquote.Replace(m[1]) != c.ds || unquote.Replace(m[2]) != c.table {
			t.Errorf("%s: %q", q, m)
		}
		// Filtered counts must run the real query.
		if countAll.MatchString(q + " WHERE `a` = ?") {
			t.Errorf("%s: matched a filtered count", q)
		}
	}
}
