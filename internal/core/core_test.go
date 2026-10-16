package core

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
)

// noTxSQLite is SQLite pretending to have no transactions, to exercise the
// one-statement-at-a-time edit path.
type noTxSQLite struct{ driver.Driver }

func (noTxSQLite) Name() string { return "sqlite-notx" }
func (n noTxSQLite) Features() driver.Features {
	f := driver.FeaturesOf(n.Driver)
	f.Transactions = false
	return f
}

func init() {
	base, err := driver.Get("sqlite")
	if err != nil {
		panic(err)
	}
	driver.Register(noTxSQLite{base})
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want any
	}{
		{math.NaN(), "NaN"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{float32(math.NaN()), "NaN"},
		{float32(math.Inf(-1)), "-Infinity"},
		{1.5, 1.5},
		{float32(2.5), float32(2.5)},
		{int64(1 << 53), int64(1 << 53)},
		{int64(1<<53 + 1), "9007199254740993"},
		{int64(-(1 << 53)), int64(-(1 << 53))},
		{int64(-(1 << 53) - 1), "-9007199254740993"},
		{int(1<<53 + 1), "9007199254740993"},
		{uint64(1 << 53), uint64(1 << 53)},
		{uint64(math.MaxUint64), "18446744073709551615"},
		{uint(1<<53 + 1), "9007199254740993"},
		{uint(7), uint(7)},
		{uint8(8), uint8(8)},
		{uint16(16), uint16(16)},
		{uint32(32), uint32(32)},
		{[]byte("text"), "text"},
		{[]byte{0xff, 0x00}, "0xff00"},
		{[16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 1, 2, 3, 4, 5, 6, 7, 8}, "12345678-9abc-def0-0102-030405060708"},
		{time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), "2024-01-02 03:04:05Z"},
		{nil, nil},
	}
	for _, c := range cases {
		if got := normalize(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("normalize(%#v) = %#v, want %#v", c.in, got, c.want)
		}
	}
	// Everything normalize returns must be encodable.
	for _, c := range cases {
		if _, err := json.Marshal(normalize(c.in)); err != nil {
			t.Errorf("json(%#v): %v", c.in, err)
		}
	}
}

func TestPlaceholderNumbering(t *testing.T) {
	pg, err := driver.Get("postgres")
	if err != nil {
		t.Fatal(err)
	}
	where, args := filterSQL(pg, "a > 1", []ColumnMatch{{"x", 1}, {"y", nil}, {"z", "q"}}, 3)
	if want := ` WHERE (a > 1) AND "x" = $3 AND "y" IS NULL AND "z" = $4`; where != want || !reflect.DeepEqual(args, []any{1, "q"}) {
		t.Errorf("filterSQL = %q %v", where, args)
	}
	if where, args := filterSQL(pg, "  ", nil, 1); where != "" || args != nil {
		t.Errorf("empty filter = %q %v", where, args)
	}
	// Keys are sorted; NULL keys take no placeholder.
	w, wargs := whereClause(pg, map[string]any{"c": 2, "a": nil, "b": 1}, 5)
	if w != `"a" IS NULL AND "b" = $5 AND "c" = $6` || !reflect.DeepEqual(wargs, []any{1, 2}) {
		t.Errorf("whereClause = %q %v", w, wargs)
	}
	if w, _ := whereClause(pg, nil, 1); w != "1 = 0" {
		t.Errorf("no keys must match nothing, got %q", w)
	}
}

func TestSplitDialects(t *testing.T) {
	script := `SELECT 'C:\'; SELECT 2`
	if got := SplitStatements(script, "postgres"); len(got) != 2 {
		t.Errorf("postgres: %q", got)
	}
	// In MySQL \' is an escaped quote, so the string runs to the end.
	if got := SplitStatements(script, "mysql"); len(got) != 1 {
		t.Errorf("mysql: %q", got)
	}
	if got := SplitStatements(`SELECT 'it\'s; fine'; SELECT "a\"; b"`, "clickhouse"); len(got) != 2 {
		t.Errorf("clickhouse: %q", got)
	}
	if got := SplitStatements("SELECT 1; /* only\n a comment; */ ; -- and this", ""); len(got) != 1 {
		t.Errorf("comments: %q", got)
	}
}

func TestReturnsRows(t *testing.T) {
	yes := []string{
		"select 1", "  (SELECT 1)", "WITH x AS (SELECT 1) SELECT * FROM x", "values (1)",
		"SHOW TABLES", "describe t", "EXPLAIN SELECT 1", "PRAGMA table_info(t)", "CALL p()",
		"EXEC sp_who", "execute p", "-- c\n/* d */ SELECT 1",
		"INSERT INTO t VALUES (1) RETURNING id", "DELETE FROM t\nRETURNING\t*",
		"INSERT INTO t OUTPUT inserted.id VALUES (1)",
	}
	no := []string{
		"INSERT INTO t VALUES ('RETURNING ')", "UPDATE t SET returning_x = 1",
		"CREATE TABLE t (id int)", "DELETE FROM t -- RETURNING", "SELECTX", "",
	}
	for _, s := range yes {
		if !returnsRows(s) {
			t.Errorf("returnsRows(%q) = false", s)
		}
	}
	for _, s := range no {
		if returnsRows(s) {
			t.Errorf("returnsRows(%q) = true", s)
		}
	}
	if !readOnly("select 1") || readOnly("ALTER TABLE t ADD c int") || readOnly("WITH d AS (DELETE FROM t) SELECT 1") {
		t.Error("readOnly")
	}
}

func TestStoreSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "connections.json")
	st := NewStore(path)
	cfg, err := st.Put(driver.Config{Name: "a", Driver: "sqlite", Password: "pw", Secrets: []string{"password"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(driver.Config{Name: "b", Driver: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"secrets"`) {
		t.Errorf("Secrets was persisted:\n%s", b)
	}
	got, err := st.Get(cfg.ID)
	if err != nil || got.Password != "pw" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if err := st.Delete(cfg.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.List(); len(list) != 1 || list[0].Name != "b" {
		t.Fatalf("after delete: %+v", list)
	}
	if _, err := st.Get(cfg.ID); err != errNotFound {
		t.Fatalf("Get deleted: %v", err)
	}
}

func TestRedactAndMerge(t *testing.T) {
	stored := driver.Config{
		ID: "x", Password: "pw", SSHPassword: "sshpw", SSHPassphrase: "phrase",
		Options: map[string]string{
			"apiToken": "tok", "accountId": "acc", "authToken": "",
			"uri": "mongodb://ada:s%3Acret@h1:27017,h2:27017/app?authSource=admin",
		},
	}
	r := redact(stored)
	if r.Password != "" || r.SSHPassword != "" || r.SSHPassphrase != "" || r.Options["apiToken"] != "" {
		t.Fatalf("secrets left: %+v", r)
	}
	if r.Options["accountId"] != "acc" || r.Options["uri"] != "mongodb://ada@h1:27017,h2:27017/app?authSource=admin" {
		t.Fatalf("non-secrets changed: %+v", r.Options)
	}
	want := []string{"password", "sshPassword", "sshPassphrase", "options.apiToken", "options.uri.password"}
	if !reflect.DeepEqual(r.Secrets, want) {
		t.Fatalf("Secrets = %v, want %v", r.Secrets, want)
	}
	if stored.Options["apiToken"] != "tok" {
		t.Fatal("redact modified the original options map")
	}

	// Editing the redacted copy without retyping keeps every secret.
	r.Name = "renamed"
	m := mergeSecrets(r, stored)
	if m.Password != "pw" || m.SSHPassword != "sshpw" || m.SSHPassphrase != "phrase" || m.Options["apiToken"] != "tok" ||
		m.Options["uri"] != stored.Options["uri"] || m.Name != "renamed" || m.Secrets != nil {
		t.Fatalf("merge: %+v", m)
	}
	// Retyped secrets win; a URI for another user doesn't get the old password.
	r.Password = "new"
	r.Options["uri"] = "mongodb://bob@h1/app"
	m = mergeSecrets(r, stored)
	if m.Password != "new" || m.Options["uri"] != "mongodb://bob@h1/app" {
		t.Fatalf("retyped: %+v", m)
	}
	if got, had := stripURIPassword("mongodb://h1/app"); had || got != "mongodb://h1/app" {
		t.Fatalf("no userinfo: %q %v", got, had)
	}
}

func TestSecretsThroughService(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(NewStore(filepath.Join(dir, "c.json")))
	t.Cleanup(svc.Shutdown)
	saved, err := svc.SaveConnection(driver.Config{Name: "s", Driver: "sqlite", FilePath: filepath.Join(dir, "t.db"), Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Password != "" || !reflect.DeepEqual(saved.Secrets, []string{"password"}) {
		t.Fatalf("SaveConnection returned secrets: %+v", saved)
	}
	list, err := svc.ListConnections()
	if err != nil || len(list) != 1 || list[0].Password != "" || !reflect.DeepEqual(list[0].Secrets, []string{"password"}) {
		t.Fatalf("ListConnections = %+v, %v", list, err)
	}
	// Save the redacted copy back: the password survives.
	list[0].Name = "s2"
	if _, err := svc.SaveConnection(list[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.store.Get(saved.ID); got.Password != "pw" || got.Name != "s2" || got.Secrets != nil {
		t.Fatalf("stored = %+v", got)
	}
	merged, err := svc.withStoredSecrets(list[0])
	if err != nil || merged.Password != "pw" {
		t.Fatalf("TestConnection would use %+v, %v", merged, err)
	}
	if _, err := svc.TestConnection(list[0]); err != nil {
		t.Fatal(err)
	}
	// An unknown ID is a new, unsaved connection: nothing to merge.
	if c, err := svc.withStoredSecrets(driver.Config{ID: "nope"}); err != nil || c.Password != "" {
		t.Fatalf("unknown id: %+v, %v", c, err)
	}
}

func TestNonTransactionalEditsNeedUniqueRow(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(NewStore(filepath.Join(dir, "c.json")))
	t.Cleanup(svc.Shutdown)
	cfg, err := svc.SaveConnection(driver.Config{Name: "n", Driver: "sqlite-notx", FilePath: filepath.Join(dir, "n.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Connect(cfg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteQuery(cfg.ID, `CREATE TABLE d (v TEXT, n INT); INSERT INTO d VALUES ('a', 1), ('a', 2), ('b', 3);`); err != nil {
		t.Fatal(err)
	}
	count := func() int64 {
		res, _ := svc.ExecuteQuery(cfg.ID, "SELECT COUNT(*) FROM d")
		return res[0].Rows[0][0].(int64)
	}
	_, err = svc.ApplyChanges(cfg.ID, ChangeSet{Table: "d", Deletes: []map[string]any{{"v": "a"}}})
	if err == nil || !strings.Contains(err.Error(), "matches 2 rows") || count() != 3 {
		t.Fatalf("delete of an ambiguous row: %v (rows %d)", err, count())
	}
	_, err = svc.ApplyChanges(cfg.ID, ChangeSet{Table: "d", Updates: []RowUpdate{{Keys: map[string]any{"v": "a"}, Values: map[string]any{"n": 9}}}})
	if err == nil || !strings.Contains(err.Error(), "matches 2 rows") {
		t.Fatalf("update of an ambiguous row: %v", err)
	}
	if _, err := svc.ApplyChanges(cfg.ID, ChangeSet{Table: "d",
		Updates: []RowUpdate{{Keys: map[string]any{"v": "b"}, Values: map[string]any{"n": 4}}},
		Deletes: []map[string]any{{"v": "a", "n": 1}},
	}); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatalf("rows %d", count())
	}
}

func TestReadRowsSkip(t *testing.T) {
	d, _ := driver.Get("sqlite")
	db, err := d.Open(driver.Config{FilePath: filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE n (i INT); INSERT INTO n VALUES (1), (2), (3), (4), (5), (6)`); err != nil {
		t.Fatal(err)
	}
	read := func(limit, skip int) ([][]any, bool) {
		rows, err := db.Query("SELECT i FROM n ORDER BY i")
		if err != nil {
			t.Fatal(err)
		}
		_, out, truncated, err := readRows(rows, limit, skip)
		if err != nil {
			t.Fatal(err)
		}
		return out, truncated
	}
	if out, tr := read(2, 3); !reflect.DeepEqual(out, [][]any{{int64(4)}, {int64(5)}}) || !tr {
		t.Errorf("limit 2 skip 3 = %v %v", out, tr)
	}
	if out, tr := read(0, 10); len(out) != 0 || tr {
		t.Errorf("skip past end = %v %v", out, tr)
	}
	if out, tr := read(0, 0); len(out) != 6 || tr {
		t.Errorf("all = %v %v", out, tr)
	}
}

func TestMetadataCacheInvalidation(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, "CREATE TABLE m (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	first, err := svc.GetTableData(id, TableDataRequest{Table: "m"})
	if err != nil || len(first.Columns) != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	if _, err := svc.ExecuteQuery(id, "ALTER TABLE m ADD COLUMN note TEXT"); err != nil {
		t.Fatal(err)
	}
	second, err := svc.GetTableData(id, TableDataRequest{Table: "m"})
	if err != nil || len(second.Columns) != 2 {
		t.Fatalf("stale columns after DDL: %+v %v", second.Columns, err)
	}
}

func TestCancelQueryCancelsEveryRun(t *testing.T) {
	sess := &session{}
	var ctxs []context.Context
	var dones []func()
	for range 3 {
		ctx, done := sess.startRun(context.Background())
		ctxs, dones = append(ctxs, ctx), append(dones, done)
	}
	sess.cancelRuns()
	for i, ctx := range ctxs {
		if ctx.Err() == nil {
			t.Errorf("run %d not canceled", i)
		}
		dones[i]()
	}
	if len(sess.running) != 0 {
		t.Errorf("runs not unregistered: %d", len(sess.running))
	}
	sess.close()
	if ctx, _ := sess.startRun(context.Background()); ctx.Err() == nil {
		t.Error("a run on a closed session must start canceled")
	}
}

func TestConcurrentUse(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, "CREATE TABLE c (id INTEGER PRIMARY KEY, v TEXT); INSERT INTO c (v) VALUES ('a'), ('b')"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.GetTableData(id, TableDataRequest{Table: "c"}); err != nil {
				t.Error(err)
			}
			// Another goroutine's CancelQuery may cancel this run.
			if _, err := svc.ExecuteQuery(id, "SELECT * FROM c"); err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
			_ = svc.CancelQuery(id)
		}()
	}
	wg.Wait()
}

func TestWithContext(t *testing.T) {
	svc, id := newSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WithContext(svc, ctx).ListTables(id); err == nil {
		t.Fatal("a canceled request context must stop the call")
	}
	// The original service, sharing the session, is unaffected.
	if _, err := svc.ListTables(id); err != nil {
		t.Fatal(err)
	}
}
