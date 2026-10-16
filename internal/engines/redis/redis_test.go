package redis

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alicebob/miniredis/v2"

	"github.com/mdenesfe/TableF/internal/driver"
)

func open(t *testing.T) (*miniredis.Miniredis, *conn) {
	t.Helper()
	mr := miniredis.RunT(t)
	host, port, _ := strings.Cut(mr.Addr(), ":")
	p, _ := strconv.Atoi(port)
	sc, err := store{}.Open(context.Background(), driver.Config{Host: host, Port: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sc.Close() })
	return mr, sc.(*conn)
}

func seed(mr *miniredis.Miniredis) {
	for i := 1; i <= 12; i++ {
		mr.Set("user:"+strconv.Itoa(i), "u"+strconv.Itoa(i))
	}
	mr.HSet("session:a", "id", "1", "name", "x")
	mr.Lpush("queue:jobs", "b")
	mr.Lpush("queue:jobs", "a")
	mr.SAdd("tags", "go", "redis")
	mr.ZAdd("rank:z", 2, "b")
	mr.ZAdd("rank:z", 1, "a")
	mr.Set("plain", "v")
	mr.SetTTL("user:1", 100*time.Second)
}

func TestRegistered(t *testing.T) {
	if _, ok := driver.GetStore("redis"); !ok {
		t.Fatal("redis store not registered")
	}
}

func TestTables(t *testing.T) {
	mr, c := open(t)
	seed(mr)
	tables, err := c.Tables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name)
		if tb.Type != "table" || tb.Schema != "" {
			t.Errorf("bad table %+v", tb)
		}
	}
	want := []string{"*", "queue", "rank", "session", "user"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tables = %v, want %v", names, want)
	}
}

func TestRowsPagingAndValues(t *testing.T) {
	mr, c := open(t)
	seed(mr)
	ctx := context.Background()
	res, err := c.Rows(ctx, driver.RowsRequest{Table: "user", Limit: 5, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 12 || len(res.Rows) != 5 {
		t.Fatalf("total %d rows %d", res.Total, len(res.Rows))
	}
	// sorted: user:1, user:10, user:11, user:12, user:2
	if res.Rows[0][0] != "user:1" || res.Rows[4][0] != "user:2" {
		t.Fatalf("order %v", res.Rows)
	}
	if res.Rows[0][1] != "string" || res.Rows[0][2] != int64(100) || res.Rows[0][3] != "u1" {
		t.Fatalf("row %v", res.Rows[0])
	}
	if res.Rows[1][2] != nil {
		t.Fatalf("ttl of persistent key should be nil, got %v", res.Rows[1][2])
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "user", Limit: 5, Offset: 10})
	if len(res.Rows) != 2 || res.Rows[0][0] != "user:8" {
		t.Fatalf("page 3 %v", res.Rows)
	}

	all, _ := c.Rows(ctx, driver.RowsRequest{Table: "*", Limit: 100})
	if all.Total != 17 {
		t.Fatalf("all total %d", all.Total)
	}
	vals := map[string]any{}
	for _, r := range all.Rows {
		vals[r[0].(string)] = r[3]
	}
	if vals["session:a"] != `{"id":"1","name":"x"}` {
		t.Errorf("hash %v", vals["session:a"])
	}
	if vals["queue:jobs"] != `["a","b"]` {
		t.Errorf("list %v", vals["queue:jobs"])
	}
	if vals["tags"] != `["go","redis"]` {
		t.Errorf("set %v", vals["tags"])
	}
	if vals["rank:z"] != `[{"member":"a","score":1},{"member":"b","score":2}]` {
		t.Errorf("zset %v", vals["rank:z"])
	}
}

func TestRowsFilterOrderMatch(t *testing.T) {
	mr, c := open(t)
	seed(mr)
	ctx := context.Background()
	res, _ := c.Rows(ctx, driver.RowsRequest{Table: "user", Filter: "user:1*", Limit: 100})
	if res.Total != 4 { // 1, 10, 11, 12
		t.Fatalf("glob total %d", res.Total)
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "*", Filter: "ser:1", Limit: 100})
	if res.Total != 4 {
		t.Fatalf("substring total %d", res.Total)
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "*", Filter: "ses", Limit: 100})
	if res.Total != 1 || res.Rows[0][0] != "session:a" {
		t.Fatalf("substring %v", res.Rows)
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "*", OrderBy: "type", OrderDir: "desc", Limit: 1})
	if res.Rows[0][1] != "zset" {
		t.Fatalf("order by type desc %v", res.Rows)
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "user", OrderBy: "key", OrderDir: "desc", Limit: 1})
	if res.Rows[0][0] != "user:9" {
		t.Fatalf("order by key desc %v", res.Rows)
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "*", Match: []driver.Match{{Column: "type", Value: "hash"}}, Limit: 10})
	if res.Total != 1 || res.Rows[0][0] != "session:a" {
		t.Fatalf("match type %v", res.Rows)
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "user", Match: []driver.Match{{Column: "ttl", Value: float64(100)}}, Limit: 10})
	if res.Total != 1 || res.Rows[0][0] != "user:1" {
		t.Fatalf("match ttl %v", res.Rows)
	}
	res, _ = c.Rows(ctx, driver.RowsRequest{Table: "user", Match: []driver.Match{{Column: "value", Value: "u7"}}, Limit: 10})
	if res.Total != 1 || res.Rows[0][0] != "user:7" {
		t.Fatalf("match value %v", res.Rows)
	}
	if !strings.Contains(res.Query, "SCAN 0 MATCH user:*") {
		t.Errorf("query %q", res.Query)
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		p, s string
		ok   bool
	}{
		{"*", "", true}, {"user:*", "user:1", true}, {"user:*", "users:1", false},
		{"h?llo", "hello", true}, {"h[ae]llo", "hallo", true}, {"h[^e]llo", "hello", false},
		{"h[a-c]llo", "hbllo", true}, {`a\*b`, "a*b", true}, {`a\*b`, "axb", false},
		{"*:1*", "user:12", true}, {"**x", "abx", true},
	}
	for _, c := range cases {
		if got := globMatch(c.p, c.s); got != c.ok {
			t.Errorf("globMatch(%q, %q) = %v", c.p, c.s, got)
		}
	}
	if escapeGlob("a*b[") != `a\*b\[` {
		t.Error(escapeGlob("a*b["))
	}
}

func TestSplitAndArgs(t *testing.T) {
	c := &conn{}
	got := c.Split("GET a\n\n# comment\n  // also\nSET b \"x y\"\n")
	if !reflect.DeepEqual(got, []string{"GET a", `SET b "x y"`}) {
		t.Fatalf("split %q", got)
	}
	args, err := parseArgs(`SET "a b" 'it\'s' "q\"\x41\n" plain`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []string{"SET", "a b", "it's", "q\"A\n", "plain"}) {
		t.Fatalf("args %q", args)
	}
	if _, err := parseArgs(`GET "a`); err == nil {
		t.Fatal("expected unbalanced quotes error")
	}
	if quoteArg("a b") != `"a b"` || quoteArg("ab") != "ab" || quoteArg("") != `""` {
		t.Fatal("quoteArg")
	}
}

func TestExecute(t *testing.T) {
	mr, c := open(t)
	seed(mr)
	ctx := context.Background()

	r, err := c.Execute(ctx, "GET plain")
	if err != nil || !reflect.DeepEqual(r.Columns, []string{"result"}) || r.Rows[0][0] != "v" {
		t.Fatalf("GET %+v %v", r, err)
	}
	r, _ = c.Execute(ctx, "GET missing")
	if len(r.Rows) != 1 || r.Rows[0][0] != nil {
		t.Fatalf("GET missing %+v", r)
	}
	r, _ = c.Execute(ctx, "LRANGE queue:jobs 0 -1")
	if !reflect.DeepEqual(r.Columns, []string{"value"}) || len(r.Rows) != 2 || r.Rows[0][0] != "a" {
		t.Fatalf("LRANGE %+v", r)
	}
	r, _ = c.Execute(ctx, "HGETALL session:a")
	if !reflect.DeepEqual(r.Columns, []string{"field", "value"}) || len(r.Rows) != 2 || r.Rows[0][0] != "id" || r.Rows[1][1] != "x" {
		t.Fatalf("HGETALL %+v", r)
	}
	r, _ = c.Execute(ctx, "DEL user:2 user:3 nope")
	if r.RowsAffected != 2 || r.Rows[0][0] != int64(2) {
		t.Fatalf("DEL %+v", r)
	}
	r, err = c.Execute(ctx, `SET "spaced key" "hello world"`)
	if err != nil || r.Rows[0][0] != "OK" {
		t.Fatalf("SET %+v %v", r, err)
	}
	if v, _ := mr.Get("spaced key"); v != "hello world" {
		t.Fatalf("stored %q", v)
	}
	if _, err := c.Execute(ctx, "NOSUCHCMD"); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := c.Execute(ctx, "select 1"); err == nil {
		t.Fatal("SELECT should be refused")
	}
	r, _ = c.Execute(ctx, "ZRANGE rank:z 0 -1 WITHSCORES")
	if len(r.Rows) == 0 {
		t.Fatalf("ZRANGE %+v", r)
	}
}

func TestApply(t *testing.T) {
	mr, c := open(t)
	seed(mr)
	ctx := context.Background()

	log, err := c.Apply(ctx, "user", driver.Changes{
		Updates: []driver.RowChange{
			{Keys: map[string]any{"key": "user:1"}, Values: map[string]any{"value": "new"}},
			{Keys: map[string]any{"key": "user:2"}, Values: map[string]any{"ttl": float64(60), "key": "user:two"}},
			{Keys: map[string]any{"key": "session:a"}, Values: map[string]any{"value": `{"id":"2","n":3}`}},
		},
		Inserts: []map[string]any{
			{"key": "user:new", "value": "fresh", "ttl": "30"},
			{"key": "tags:new", "type": "set", "value": `["a","b"]`},
		},
		Deletes: []map[string]any{{"key": "user:3"}, {"key": "user:4"}},
	})
	if err != nil {
		t.Fatal(err, log)
	}
	if v, _ := mr.Get("user:1"); v != "new" {
		t.Errorf("user:1 = %q", v)
	}
	if mr.TTL("user:1") != 100*time.Second {
		t.Errorf("ttl of user:1 not kept: %v", mr.TTL("user:1"))
	}
	if mr.Exists("user:2") || !mr.Exists("user:two") || mr.TTL("user:two") != 60*time.Second {
		t.Errorf("rename/expire failed: %v", mr.TTL("user:two"))
	}
	if mr.HGet("session:a", "id") != "2" || mr.HGet("session:a", "n") != "3" || mr.HGet("session:a", "name") != "" {
		t.Errorf("hash rewrite failed")
	}
	if v, _ := mr.Get("user:new"); v != "fresh" || mr.TTL("user:new") != 30*time.Second {
		t.Errorf("insert failed %q %v", v, mr.TTL("user:new"))
	}
	if m, _ := mr.Members("tags:new"); !reflect.DeepEqual(m, []string{"a", "b"}) {
		t.Errorf("set insert %v", m)
	}
	if mr.Exists("user:3") || mr.Exists("user:4") {
		t.Errorf("delete failed")
	}
	joined := strings.Join(log, "\n")
	for _, want := range []string{"SET user:1 new", "PEXPIRE user:1 100000", "EXPIRE user:2 60", "RENAMENX user:2 user:two", "HSET session:a id 2 n 3", "SET user:new fresh NX EX 30", "SADD tags:new a b", "DEL user:3 user:4"} {
		if !strings.Contains(joined, want) {
			t.Errorf("log missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(strings.ToUpper(joined), "AUTH") {
		t.Error("log must not contain AUTH")
	}

	// Inserting an existing key fails; changing the type fails; persist.
	if _, err := c.Apply(ctx, "user", driver.Changes{Inserts: []map[string]any{{"key": "user:1", "value": "x"}}}); err == nil {
		t.Error("insert over an existing key should fail")
	}
	if _, err := c.Apply(ctx, "user", driver.Changes{Updates: []driver.RowChange{{Keys: map[string]any{"key": "user:1"}, Values: map[string]any{"type": "hash"}}}}); err == nil {
		t.Error("type change should fail")
	}
	if _, err := c.Apply(ctx, "user", driver.Changes{Updates: []driver.RowChange{{Keys: map[string]any{"key": "user:1"}, Values: map[string]any{"ttl": nil}}}}); err != nil {
		t.Error(err)
	}
	if mr.TTL("user:1") != 0 {
		t.Errorf("persist failed %v", mr.TTL("user:1"))
	}
	if _, err := c.Apply(ctx, "user", driver.Changes{Updates: []driver.RowChange{{Keys: map[string]any{"key": "user:5"}, Values: map[string]any{"key": "user:6"}}}}); err == nil {
		t.Error("rename onto an existing key should fail")
	}
}

func TestDatabasesVersionFeatures(t *testing.T) {
	_, c := open(t)
	dbs, _ := c.Databases(context.Background())
	if len(dbs) < 1 || dbs[0] != "0" {
		t.Fatalf("dbs %v", dbs)
	}
	if v := c.Version(context.Background()); !strings.HasPrefix(v, "Redis") {
		t.Fatalf("version %q", v)
	}
	f := c.Features()
	if f.Family != "redis" || !f.Edit || f.Schemas || f.Groups {
		t.Fatalf("features %+v", f)
	}
}

func TestGlobIsLinear(t *testing.T) {
	// The recursive matcher took exponential time on patterns like this.
	p := strings.Repeat("*a", 30) + "b"
	s := strings.Repeat("a", 200)
	start := time.Now()
	if globMatch(p, s) {
		t.Fatal("should not match")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("globMatch took %v", d)
	}
	if !globMatch("*a*b*c", "xxaxxbxxc") || globMatch("*a*b*c", "xxaxxcxxb") {
		t.Fatal("backtracking")
	}
}

func TestLargeValuesAreCapped(t *testing.T) {
	mr, c := open(t)
	ctx := context.Background()
	big := strings.Repeat("é", maxStringBytes) // 2 bytes each
	mr.Set("big", big)
	for i := 0; i < maxItems+50; i++ {
		mr.HSet("h", "f"+strconv.Itoa(1000+i), "v")
	}
	res, err := c.Rows(ctx, driver.RowsRequest{Table: "*", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, r := range res.Rows {
		vals[r[0].(string)] = r[3].(string)
	}
	if v := vals["big"]; len(v) > maxStringBytes+100 || !strings.HasSuffix(v, "… (131072 bytes, truncated)") || !utf8.ValidString(v) {
		t.Fatalf("big string: %d bytes, suffix %q", len(v), v[max(0, len(v)-40):])
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(vals["h"]), &fields); err != nil || len(fields) > maxItems {
		t.Fatalf("hash: %d fields, %v", len(fields), err)
	}
	// Truncated values can't be overwritten from the grid.
	for _, key := range []string{"big", "h"} {
		_, err := c.Apply(ctx, "*", driver.Changes{Updates: []driver.RowChange{{Keys: map[string]any{"key": key}, Values: map[string]any{"value": `{"a":"b"}`}}}})
		if err == nil || !strings.Contains(err.Error(), "can't be edited here") {
			t.Fatalf("%s: %v", key, err)
		}
	}
}

func TestKeyScanCache(t *testing.T) {
	mr, c := open(t)
	ctx := context.Background()
	mr.Set("a", "1")
	if res, _ := c.Rows(ctx, driver.RowsRequest{Table: "*", Limit: 10}); res.Total != 1 {
		t.Fatalf("total %d", res.Total)
	}
	mr.Set("b", "2") // written behind the cache's back: not seen yet
	if res, _ := c.Rows(ctx, driver.RowsRequest{Table: "*", Limit: 10}); res.Total != 1 {
		t.Fatalf("cached total %d", res.Total)
	}
	// Writes through the connection drop the cache.
	if _, err := c.Execute(ctx, "SET c 3"); err != nil {
		t.Fatal(err)
	}
	if res, _ := c.Rows(ctx, driver.RowsRequest{Table: "*", Limit: 10, OrderBy: "key", OrderDir: "desc"}); res.Total != 3 || res.Rows[0][0] != "c" {
		t.Fatalf("after write: %v", res.Rows)
	}
	// Reversing a page must not corrupt the cached (shared) key list.
	if res, _ := c.Rows(ctx, driver.RowsRequest{Table: "*", Limit: 10}); res.Rows[0][0] != "a" {
		t.Fatalf("cache corrupted: %v", res.Rows)
	}
}
