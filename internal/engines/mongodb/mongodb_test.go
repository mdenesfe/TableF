package mongodb

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/mdenesfe/TableF/internal/driver"
)

func TestRegistered(t *testing.T) {
	if _, ok := driver.GetStore("mongodb"); !ok {
		t.Fatal("mongodb store not registered")
	}
}

func TestRelaxedToJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{}`, `{}`},
		{`{age: {$gt: 30}, name: "x"}`, `{"age":{"$gt":30},"name":"x"}`},
		{`{name: 'it\'s', "q": 'say "hi"'}`, `{"name":"it's","q":"say \"hi\""}`},
		{`{a.b: 1, 2: true, _x$: null}`, `{"a.b":1,"2":true,"_x$":null}`},
		{`{a: [1, 2, 3,], b: {c: -1.5e3,},}`, `{"a":[1,2,3],"b":{"c":-1.5e3}}`},
		{`{a: .5, b: 5., c: +3, d: 007}`, `{"a":0.5,"b":5.0,"c":3,"d":7}`},
		{"{ // comment\n a: 1 /* inline */ }", `{"a":1}`},
		{`{_id: ObjectId("5F1A2b3c4d5e6f7a8b9c0d1e")}`, `{"_id":{"$oid":"5f1a2b3c4d5e6f7a8b9c0d1e"}}`},
		{`{d: ISODate("2024-01-02")}`, `{"d":{"$date":"2024-01-02T00:00:00Z"}}`},
		{`{d: new Date("2024-01-02T03:04:05.123Z")}`, `{"d":{"$date":"2024-01-02T03:04:05.123Z"}}`},
		{`{n: NumberLong(42), i: NumberInt("7"), m: NumberDecimal("1.10")}`, `{"n":{"$numberLong":"42"},"i":{"$numberInt":"7"},"m":{"$numberDecimal":"1.10"}}`},
		{`{name: /^ab\/c/i}`, `{"name":{"$regularExpression":{"pattern":"^ab/c","options":"i"}}}`},
		{`{u: undefined, x: Infinity, y: -Infinity}`, `{"u":null,"x":{"$numberDouble":"Infinity"},"y":{"$numberDouble":"-Infinity"}}`},
		{`{"$or": [{a: 1}, {b: {$in: ["x", 'y']}}]}`, `{"$or":[{"a":1},{"b":{"$in":["x","y"]}}]}`},
		{`{s: "tab\there", e: "é"}`, `{"s":"tab\there","e":"é"}`},
		{`{k: MinKey, l: MaxKey()}`, `{"k":{"$minKey":1},"l":{"$maxKey":1}}`},
		{`[1, "a"]`, `[1,"a"]`},
	}
	for _, c := range cases {
		got, err := RelaxedToJSON(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	for _, bad := range []string{`{a: }`, `{a 1}`, `{a: 'x}`, `{a: foo}`, `{a: ObjectId("zz")}`, `{a: 1} x`, `{a: unknownFn(1)}`} {
		if _, err := RelaxedToJSON(bad); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
}

func TestParseDoc(t *testing.T) {
	d, err := parseDoc(`{age: {$gt: 30}, _id: ObjectId("5f1a2b3c4d5e6f7a8b9c0d1e"), tags: ['a', 'b'], at: ISODate("2024-01-02T03:04:05Z"), n: NumberLong(5)}`)
	if err != nil {
		t.Fatal(err)
	}
	if d[0].Key != "age" {
		t.Fatalf("order lost: %v", d)
	}
	gt, ok := d[0].Value.(bson.D)
	if !ok || gt[0].Key != "$gt" || gt[0].Value != int32(30) {
		t.Fatalf("nested %T %v", d[0].Value, d[0].Value)
	}
	if _, ok := d[1].Value.(bson.ObjectID); !ok {
		t.Fatalf("_id %T", d[1].Value)
	}
	if a, ok := d[2].Value.(bson.A); !ok || len(a) != 2 {
		t.Fatalf("array %T", d[2].Value)
	}
	if dt, ok := d[3].Value.(bson.DateTime); !ok || dt.Time().UTC().Hour() != 3 {
		t.Fatalf("date %T %v", d[3].Value, d[3].Value)
	}
	if d[4].Value != int64(5) {
		t.Fatalf("long %T", d[4].Value)
	}
	if e, err := parseDoc("   "); err != nil || len(e) != 0 {
		t.Fatal("empty filter should be {}")
	}
	if _, err := parseDoc(`[1]`); err == nil {
		t.Fatal("array filter should fail")
	}
}

func TestSplitScript(t *testing.T) {
	script := `db.users.find({a: 1})
db.users.find({
  name: "x;y",
  re: /a;b/,
})
  .sort({name: 1})
// comment; with semicolon
db.orders.countDocuments(); db.x.insertOne({s: 'it\'s; fine'})
{ping: 1}

db.getCollection("a-b").find()`
	got := splitScript(script)
	want := []string{
		`db.users.find({a: 1})`,
		"db.users.find({\n  name: \"x;y\",\n  re: /a;b/,\n})\n  .sort({name: 1})",
		`db.orders.countDocuments()`,
		`db.x.insertOne({s: 'it\'s; fine'})`,
		`{ping: 1}`,
		`db.getCollection("a-b").find()`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %d commands:\n%s", len(got), strings.Join(got, "\n---\n"))
	}
	if got := splitScript("  \n// only a comment\n;;"); len(got) != 0 {
		t.Fatalf("expected nothing, got %q", got)
	}
}

func TestParseShell(t *testing.T) {
	c, err := parseShell(`db.users.find({age: {$gt: 30}}, {name: 1}).sort({name: -1}).skip(10).limit(5);`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "collection" || c.Coll != "users" || c.Method != "find" || len(c.Args) != 2 || len(c.Chain) != 3 {
		t.Fatalf("%+v", c)
	}
	if c.Args[0] != `{"age":{"$gt":30}}` || c.Chain[0].Method != "sort" || c.Chain[2].Args[0] != "5" {
		t.Fatalf("%+v", c)
	}

	c, err = parseShell(`db.getCollection("my-coll.v2").deleteMany({})`)
	if err != nil || c.Coll != "my-coll.v2" || c.Method != "deleteMany" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = parseShell(`db["odd name"].findOne()`)
	if err != nil || c.Coll != "odd name" || c.Method != "findOne" || len(c.Args) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = parseShell(`db.system.profile.find()`)
	if err != nil || c.Coll != "system.profile" || c.Method != "find" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = parseShell(`db.getCollectionNames()`)
	if err != nil || c.Kind != "db" || c.Method != "getCollectionNames" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = parseShell(`db.orders.aggregate([{$match: {s: "A"}}, {$group: {_id: "$c", n: {$sum: 1}}}])`)
	if err != nil || c.Method != "aggregate" || len(c.Args) != 1 || !strings.HasPrefix(c.Args[0], `[{"$match"`) {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = parseShell(`{ping: 1}`)
	if err != nil || c.Kind != "command" || c.Args[0] != `{"ping":1}` {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = parseShell(`show collections`)
	if err != nil || c.Kind != "show" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = parseShell(`db.u.distinct("city", {age: {$gte: 18}})`)
	if err != nil || len(c.Args) != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{`users.find()`, `db.users`, `db.users.find(`, `db.users.find() extra`, `db.users.find({a: })`} {
		if _, err := parseShell(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestToValue(t *testing.T) {
	oid, _ := bson.ObjectIDFromHex("5f1a2b3c4d5e6f7a8b9c0d1e")
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	dec, _ := bson.ParseDecimal128("12.50")
	cases := []struct {
		in   any
		want any
	}{
		{nil, nil},
		{bson.Null{}, nil},
		{"s", "s"},
		{int32(3), int32(3)},
		{int64(3), int64(3)},
		{1.5, 1.5},
		{true, true},
		{oid, "5f1a2b3c4d5e6f7a8b9c0d1e"},
		{bson.NewDateTimeFromTime(when), when},
		{dec, "12.50"},
		{bson.Binary{Subtype: 4, Data: []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}}, "12345678-9abc-def0-1234-56789abcdef0"},
		{bson.Binary{Subtype: 0, Data: []byte{1, 2}}, "0x0102"},
		{bson.Timestamp{T: 5, I: 1}, "Timestamp(5, 1)"},
		{bson.Regex{Pattern: "^a", Options: "i"}, "/^a/i"},
		{bson.D{{Key: "a", Value: int32(1)}, {Key: "id", Value: oid}}, `{"a":1,"id":{"$oid":"5f1a2b3c4d5e6f7a8b9c0d1e"}}`},
		{bson.A{"x", int32(2)}, `["x",2]`},
		{bson.M{"k": "v"}, `{"k":"v"}`},
	}
	for _, c := range cases {
		got := toValue(c.in)
		if gt, ok := got.(time.Time); ok {
			if !gt.Equal(c.want.(time.Time)) {
				t.Errorf("%v: got %v", c.in, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%T %v: got %T %v, want %v", c.in, c.in, got, got, c.want)
		}
	}
}

func TestTabulateAndColumns(t *testing.T) {
	oid := bson.NewObjectID()
	docs := []bson.D{
		{{Key: "name", Value: "a"}, {Key: "_id", Value: oid}, {Key: "age", Value: int32(3)}},
		{{Key: "_id", Value: oid}, {Key: "age", Value: 4.5}, {Key: "tags", Value: bson.A{"x"}}},
		{{Key: "_id", Value: oid}, {Key: "name", Value: int32(1)}, {Key: "age", Value: nil}},
	}
	cols, rows := tabulate(docs)
	if !reflect.DeepEqual(cols, []string{"_id", "name", "age", "tags"}) {
		t.Fatalf("cols %v", cols)
	}
	if rows[0][3] != nil || rows[1][1] != nil || rows[1][3] != `["x"]` || rows[0][0] != oid.Hex() {
		t.Fatalf("rows %v", rows)
	}
	fs := newFieldSet()
	for _, d := range docs {
		fs.add(d)
	}
	dc := fs.columns()
	kinds := map[string]string{}
	for _, c := range dc {
		kinds[c.Name] = c.Type
		if !c.Nullable || c.PrimaryKey != (c.Name == "_id") {
			t.Errorf("column flags %+v", c)
		}
	}
	if kinds["_id"] != "objectId" || kinds["name"] != "mixed" || kinds["age"] != "double" || kinds["tags"] != "array" {
		t.Fatalf("kinds %v", kinds)
	}
	// Documents without _id (aggregate output) don't get an empty _id column.
	cols, _ = tabulate([]bson.D{{{Key: "n", Value: int32(1)}}})
	if !reflect.DeepEqual(cols, []string{"n"}) {
		t.Fatalf("cols %v", cols)
	}
}

func TestShellRendering(t *testing.T) {
	oid, _ := bson.ObjectIDFromHex("5f1a2b3c4d5e6f7a8b9c0d1e")
	d := bson.D{
		{Key: "_id", Value: oid},
		{Key: "$set", Value: bson.D{{Key: "n", Value: int32(1)}, {Key: "s", Value: `q"x`}, {Key: "t", Value: bson.NewDateTimeFromTime(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))}}},
		{Key: "a", Value: bson.A{true, nil, 2.5}},
	}
	want := `{"_id": ObjectId("5f1a2b3c4d5e6f7a8b9c0d1e"), "$set": {"n": 1, "s": "q\"x", "t": ISODate("2024-01-02T00:00:00.000Z")}, "a": [true, null, 2.5]}`
	if got := shell(d); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if collRef("users") != "db.users" || collRef("a-b") != `db.getCollection("a-b")` || collRef("1x") != `db.getCollection("1x")` {
		t.Fatal("collRef")
	}
}

func TestRowsFilter(t *testing.T) {
	f, err := rowsFilter(`{age: {$gt: 30}}`, []driver.Match{{Column: "_id", Value: "5f1a2b3c4d5e6f7a8b9c0d1e"}, {Column: "x", Value: nil}})
	if err != nil {
		t.Fatal(err)
	}
	s := shell(f)
	want := `{"$and": [{"age": {"$gt": 30}}, {"_id": ObjectId("5f1a2b3c4d5e6f7a8b9c0d1e")}, {"x": null}]}`
	if s != want {
		t.Fatalf("got %s", s)
	}
	f, _ = rowsFilter("", []driver.Match{{Column: "n", Value: "a"}})
	if shell(f) != `{"n": "a"}` {
		t.Fatalf("got %s", shell(f))
	}
	if _, err := rowsFilter("{a:", nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCoerce(t *testing.T) {
	oid := bson.NewObjectID()
	check := func(v any, kind string, like any, want any) {
		t.Helper()
		got, err := coerce("f", v, kind, like)
		if err != nil {
			t.Fatalf("%v → %s: %v", v, kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%v → %s: got %T %v want %T %v", v, kind, got, got, want, want)
		}
	}
	check("42", "int", int32(1), int32(42))
	check("42", "int", int64(1), int64(42))
	check(float64(7), "int", nil, int32(7))
	check("1.5", "double", nil, 1.5)
	check("true", "bool", nil, true)
	check(oid.Hex(), "objectId", nil, oid)
	check("2024-01-02 03:04:05Z", "date", nil, bson.NewDateTimeFromTime(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)))
	check(`{a: 1}`, "object", nil, bson.D{{Key: "a", Value: int32(1)}})
	check(`[1, "x"]`, "array", nil, bson.A{int32(1), "x"})
	check(`{"a":1}`, "string", nil, `{"a":1}`) // strings stay strings
	check(`{"a":1}`, "", nil, bson.D{{Key: "a", Value: int32(1)}})
	check("abc", "int", nil, "abc") // not a number: kept as text
	check(nil, "int", nil, nil)
	if _, err := coerce("f", "{bad", "object", nil); err == nil {
		t.Fatal("expected an error for a broken object")
	}
}

func TestIDCandidates(t *testing.T) {
	c := idCandidates("5f1a2b3c4d5e6f7a8b9c0d1e")
	if len(c) != 2 {
		t.Fatalf("%v", c)
	}
	if _, ok := c[0].(bson.ObjectID); !ok || c[1] != "5f1a2b3c4d5e6f7a8b9c0d1e" {
		t.Fatalf("%v", c)
	}
	c = idCandidates("12345678-9abc-def0-1234-56789abcdef0")
	if len(c) != 3 {
		t.Fatalf("%v", c)
	}
	if c := idCandidates(float64(3)); len(c) != 1 || c[0] != float64(3) {
		t.Fatalf("%v", c)
	}
}

func TestBuildURI(t *testing.T) {
	u, _ := buildURI(driver.Config{Host: "h", User: "me", Password: "p@ss/w", Options: map[string]string{"tls": "true"}})
	if !strings.HasPrefix(u, "mongodb://me:p%40ss%2Fw@h:27017/?") || !strings.Contains(u, "authSource=admin") || !strings.Contains(u, "tls=true") {
		t.Fatalf("uri %s", u)
	}
	u, _ = buildURI(driver.Config{Host: "h", Port: 1, Options: map[string]string{"uri": "mongodb+srv://x.example.net/db"}})
	if u != "mongodb+srv://x.example.net/db" {
		t.Fatalf("uri %s", u)
	}
	u, _ = buildURI(driver.Config{})
	if u != "mongodb://localhost:27017/" {
		t.Fatalf("uri %s", u)
	}
}

func TestFeatures(t *testing.T) {
	f := (&conn{}).Features()
	if f.Family != "mongodb" || !f.Edit || f.Schemas || f.Groups || f.ForeignKeys || f.Routines || f.Transactions {
		t.Fatalf("%+v", f)
	}
}
