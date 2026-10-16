package cassandra

import (
	"bytes"
	"math/big"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"gopkg.in/inf.v0"

	"github.com/mdenesfe/TableF/internal/driver"
)

func native(t gocql.Type) gocql.TypeInfo { return gocql.NewNativeType(4, t, "") }

func TestRegistered(t *testing.T) {
	d, err := driver.Get("cassandra")
	if err != nil {
		t.Fatal(err)
	}
	f := driver.FeaturesOf(d)
	if f.Family != "cassandra" || f.Transactions || f.Groups || !f.Edit || !f.Schemas {
		t.Fatalf("features: %+v", f)
	}
	if got := driver.QualifiedName(d, "ks", `we"ird`); got != `"ks"."we""ird"` {
		t.Fatal(got)
	}
}

func TestPaginate(t *testing.T) {
	q, skip := engine{}.Paginate(`SELECT * FROM "t"`, 100, 300)
	if q != `SELECT * FROM "t" LIMIT 400` || skip != 300 {
		t.Fatal(q, skip)
	}
}

func TestSplitHosts(t *testing.T) {
	got := splitHosts(" a, b:9999 ,,[::1]", 9042)
	want := []string{"a:9042", "b:9999", "[::1]:9042"}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
	if h := splitHosts("", 9042); h[0] != "127.0.0.1:9042" {
		t.Fatal(h)
	}
}

func TestConsistency(t *testing.T) {
	if c, _ := parseConsistency(""); c != gocql.LocalQuorum {
		t.Fatal(c)
	}
	if c, _ := parseConsistency("local_one"); c != gocql.LocalOne {
		t.Fatal(c)
	}
	if _, err := parseConsistency("bogus"); err == nil {
		t.Fatal("expected error")
	}
}

func marshal(t *testing.T, info gocql.TypeInfo, v any) []byte {
	t.Helper()
	b, err := flexArg{v}.MarshalCQL(info)
	if err != nil {
		t.Fatalf("marshal %T into %v: %v", v, info.Type(), err)
	}
	return b
}

func TestFlexArgScalars(t *testing.T) {
	cases := []struct {
		typ  gocql.Type
		in   any
		same any // the natively typed equivalent
	}{
		{gocql.TypeInt, float64(42), int32(42)},
		{gocql.TypeInt, "42", int32(42)},
		{gocql.TypeBigInt, float64(1 << 40), int64(1 << 40)},
		{gocql.TypeSmallInt, float64(7), int16(7)},
		{gocql.TypeTinyInt, float64(-3), int8(-3)},
		{gocql.TypeDouble, "1.5", 1.5},
		{gocql.TypeFloat, 2.5, float32(2.5)},
		{gocql.TypeBoolean, "true", true},
		{gocql.TypeUUID, "550e8400-e29b-41d4-a716-446655440000", mustUUID("550e8400-e29b-41d4-a716-446655440000")},
		{gocql.TypeTimestamp, "2024-01-02 03:04:05Z", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
		{gocql.TypeTimestamp, "1700000000000", int64(1700000000000)},
		{gocql.TypeDate, "2024-01-02", time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{gocql.TypeBlob, "0xdeadbeef", []byte{0xde, 0xad, 0xbe, 0xef}},
		{gocql.TypeText, "hi", "hi"},
		{gocql.TypeVarint, "123456789012345678901234567890", *mustBig("123456789012345678901234567890")},
		{gocql.TypeDecimal, "12.345", *inf.NewDec(12345, 3)},
	}
	for _, c := range cases {
		info := native(c.typ)
		got := marshal(t, info, c.in)
		want, err := gocql.Marshal(info, c.same)
		if err != nil {
			t.Fatalf("reference marshal %v: %v", c.typ, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%v %v: got %x want %x", c.typ, c.in, got, want)
		}
	}
	if b := marshal(t, native(gocql.TypeInt), nil); b != nil {
		t.Errorf("nil should marshal to null, got %x", b)
	}
	if _, err := (flexArg{1.5}).MarshalCQL(native(gocql.TypeInt)); err == nil {
		t.Error("expected error for fractional int")
	}
	if _, err := (flexArg{float64(1 << 40)}).MarshalCQL(native(gocql.TypeInt)); err == nil {
		t.Error("expected range error")
	}
}

func mustUUID(s string) gocql.UUID {
	u, err := gocql.ParseUUID(s)
	if err != nil {
		panic(err)
	}
	return u
}

func mustBig(s string) *big.Int {
	b, _ := new(big.Int).SetString(s, 10)
	return b
}

func parsed(t *testing.T, cql string) gocql.TypeInfo {
	t.Helper()
	var info gocql.TypeInfo
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("cannot build %s: %v", cql, r)
			}
		}()
		info = gocql.NewNativeType(4, gocql.TypeCustom, cql)
	}()
	return info
}

func TestFlexArgCollections(t *testing.T) {
	info := parsed(t, "list<int>")
	if info.Type() != gocql.TypeList {
		t.Fatalf("list<int> parsed as %v", info.Type())
	}
	got := marshal(t, info, "[1, 2, 3]")
	want, _ := gocql.Marshal(info, []int32{1, 2, 3})
	if !bytes.Equal(got, want) {
		t.Errorf("list: got %x want %x", got, want)
	}
	minfo := parsed(t, "map<text, bigint>")
	got = marshal(t, minfo, map[string]any{"a": float64(1)})
	want, _ = gocql.Marshal(minfo, map[string]int64{"a": 1})
	if !bytes.Equal(got, want) {
		t.Errorf("map: got %x want %x", got, want)
	}
	if typeName(minfo) == "" || typeName(native(gocql.TypeInt)) == "" {
		t.Error("empty type names")
	}
	if got := typeName(minfo); got != "map<text, bigint>" {
		t.Errorf("type name %q", got)
	}
}

func TestToJSONValue(t *testing.T) {
	u := mustUUID("550e8400-e29b-41d4-a716-446655440000")
	cases := []struct {
		in   any
		want any
	}{
		{int32(5), int64(5)},
		{float32(1.5), 1.5},
		{u, "550e8400-e29b-41d4-a716-446655440000"},
		{[]byte{1, 2}, "0x0102"},
		{inf.NewDec(15, 1), "1.5"},
		{mustBig("12345678901234567890"), "12345678901234567890"},
		{[]int{1, 2}, "[1,2]"},
		{map[int]string{1: "a"}, `{"1":"a"}`},
		{[]gocql.UUID{u}, `["550e8400-e29b-41d4-a716-446655440000"]`},
		{map[string]any{"x": int32(1), "y": nil}, `{"x":1,"y":null}`},
	}
	for _, c := range cases {
		if got := toJSONValue(c.in); got != c.want {
			t.Errorf("%T %v: got %#v want %#v", c.in, c.in, got, c.want)
		}
	}
}

func TestSortColumns(t *testing.T) {
	cols := []cqlColumn{
		{Column: driver.Column{Name: "z"}, kind: "regular", pos: -1},
		{Column: driver.Column{Name: "c2"}, kind: "clustering", pos: 1},
		{Column: driver.Column{Name: "a"}, kind: "regular", pos: -1},
		{Column: driver.Column{Name: "pk"}, kind: "partition_key", pos: 0},
		{Column: driver.Column{Name: "c1"}, kind: "clustering", pos: 0},
		{Column: driver.Column{Name: "s"}, kind: "static", pos: -1},
	}
	got := sortColumns(cols)
	want := []string{"pk", "c1", "c2", "s", "a", "z"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("order: %v", got)
		}
	}
}
