package core

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
)

func TestSplitStatements(t *testing.T) {
	got := SplitStatements(`
		-- comment; not a split
		SELECT 'a;b', "x;y" FROM t;
		/* block; */ INSERT INTO t VALUES (1);
		CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql;
		;
	`, "postgres")
	want := []string{
		"-- comment; not a split\n\t\tSELECT 'a;b', \"x;y\" FROM t",
		"/* block; */ INSERT INTO t VALUES (1)",
		"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func newSQLite(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	svc := NewService(NewStore(filepath.Join(dir, "conn.json")))
	t.Cleanup(svc.Shutdown)
	cfg, err := svc.SaveConnection(driver.Config{Name: "test", Driver: "sqlite", FilePath: filepath.Join(dir, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Connect(cfg.ID); err != nil {
		t.Fatal(err)
	}
	return svc, cfg.ID
}

func TestQueryAndEdit(t *testing.T) {
	svc, id := newSQLite(t)

	res, err := svc.ExecuteQuery(id, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT);
		INSERT INTO users (name) VALUES ('ada'), ('linus');
		SELECT * FROM users ORDER BY id;`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[1].RowsAffected != 2 || len(res[2].Rows) != 2 || res[2].Error != "" {
		t.Fatalf("unexpected results: %+v", res)
	}
	if got := res[2].ColumnTypes; len(got) != 3 || got[0] != "integer" || got[1] != "text" {
		t.Fatalf("column types: %v", got)
	}

	tables, _ := svc.ListTables(id)
	if len(tables) != 1 || tables[0].Name != "users" {
		t.Fatalf("tables: %+v", tables)
	}

	_, err = svc.ApplyChanges(id, ChangeSet{
		Table:   "users",
		Updates: []RowUpdate{{Keys: map[string]any{"id": 1}, Values: map[string]any{"note": "first"}}},
		Inserts: []map[string]any{{"name": "grace"}},
		Deletes: []map[string]any{{"id": 2}},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := svc.GetTableData(id, TableDataRequest{Table: "users", OrderBy: "id", Where: "name <> 'nobody'"})
	if err != nil {
		t.Fatal(err)
	}
	if data.Total != 2 || data.Rows[0][2] != "first" || data.Rows[1][1] != "grace" {
		t.Fatalf("data: %+v", data)
	}
	if !data.Columns[0].PrimaryKey {
		t.Fatal("id should be primary key")
	}
}

func TestGroups(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, `
		CREATE TABLE p (id INTEGER PRIMARY KEY, city TEXT, team TEXT);
		INSERT INTO p (city, team) VALUES ('A','x'),('A','y'),('A','x'),('B','x'),(NULL,'z');`); err != nil {
		t.Fatal(err)
	}
	g, err := svc.GetGroups(id, GroupRequest{Table: "p", Column: "city"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Total != 3 || g.Rows != 5 || g.Groups[0].Value != "A" || g.Groups[0].Count != 3 || g.Groups[2].Value != nil {
		t.Fatalf("groups: %+v", g)
	}
	// Second level inside city = A, then the rows of A/x.
	g2, err := svc.GetGroups(id, GroupRequest{Table: "p", Column: "team", Match: []ColumnMatch{{"city", "A"}}})
	if err != nil || len(g2.Groups) != 2 || g2.Groups[0].Value != "x" || g2.Groups[0].Count != 2 {
		t.Fatalf("level 2: %+v %v", g2, err)
	}
	data, err := svc.GetTableData(id, TableDataRequest{Table: "p", Where: "id > 0", Match: []ColumnMatch{{"city", "A"}, {"team", "x"}}})
	if err != nil || data.Total != 2 {
		t.Fatalf("rows: %+v %v", data, err)
	}
	ordered, err := svc.GetTableData(id, TableDataRequest{Table: "p", OrderSQL: "city DESC NULLS LAST, id"})
	if err != nil || ordered.Rows[0][1] != "B" {
		t.Fatalf("raw order: %+v %v", ordered, err)
	}
	nulls, err := svc.GetTableData(id, TableDataRequest{Table: "p", Match: []ColumnMatch{{"city", nil}}})
	if err != nil || nulls.Total != 1 {
		t.Fatalf("null group: %+v %v", nulls, err)
	}
}

func TestForeignKeys(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, `
		CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INT REFERENCES customers(id), total REAL);
		CREATE TABLE notes (id INTEGER PRIMARY KEY, order_id INT REFERENCES orders);`); err != nil {
		t.Fatal(err)
	}
	o, err := svc.GetTableData(id, TableDataRequest{Table: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.ForeignKeys) != 1 || o.ForeignKeys[0].Column != "customer_id" || o.ForeignKeys[0].RefTable != "customers" || o.ForeignKeys[0].RefColumn != "id" {
		t.Fatalf("out: %+v", o.ForeignKeys)
	}
	// notes.order_id REFERENCES orders (no column) resolves to orders' primary key.
	if len(o.ReferencedBy) != 1 || o.ReferencedBy[0].Table != "notes" || o.ReferencedBy[0].RefColumn != "id" {
		t.Fatalf("in: %+v", o.ReferencedBy)
	}
}

func TestApplyChangesRollsBack(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT NOT NULL); INSERT INTO t VALUES (1, 'a');`); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ApplyChanges(id, ChangeSet{
		Table:   "t",
		Updates: []RowUpdate{{Keys: map[string]any{"id": 1}, Values: map[string]any{"v": "b"}}},
		Inserts: []map[string]any{{"id": 2}}, // violates NOT NULL
	})
	if err == nil {
		t.Fatal("expected error")
	}
	res, _ := svc.ExecuteQuery(id, "SELECT v FROM t")
	if res[0].Rows[0][0] != "a" {
		t.Fatalf("update was not rolled back: %v", res[0].Rows)
	}
}

func TestCatalog(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, `CREATE TABLE a (id INTEGER PRIMARY KEY); CREATE VIEW v AS SELECT * FROM a;`); err != nil {
		t.Fatal(err)
	}
	cat, err := svc.GetCatalog(id, "")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, tb := range cat.Tables {
		kinds[tb.Name] = tb.Type
	}
	if kinds["a"] != "table" || kinds["v"] != "view" {
		t.Fatalf("tables: %+v", cat.Tables)
	}
	if cat.Security {
		t.Fatal("SQLite has no users or roles to list")
	}
	p, err := svc.ListPrincipals(id, "")
	if err != nil || len(p) != 0 {
		t.Fatalf("principals: %v %v", p, err)
	}
}

func TestTableInfo(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, `CREATE TABLE a (id INTEGER PRIMARY KEY AUTOINCREMENT, n TEXT); CREATE INDEX a_n ON a(n); INSERT INTO a (n) VALUES ('x');`); err != nil {
		t.Fatal(err)
	}
	fields, err := svc.GetTableInfo(id, "", "a")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range fields {
		if f.Value != nil {
			got[f.Key] = *f.Value
		}
	}
	if got["kind"] != "table" || got["index_count"] != "1" || got["autoincrement"] != "yes" || got["last_autoincrement"] != "1" {
		t.Fatalf("fields: %v", got)
	}
	if _, err := svc.GetTableInfo(id, "", "missing"); err == nil {
		t.Fatal("expected an error for a missing table")
	}
}

func TestSavedQueries(t *testing.T) {
	svc := NewService(NewStore(filepath.Join(t.TempDir(), "connections.json")))
	if _, err := svc.SaveQuery(SavedQuery{Name: "  "}); err == nil {
		t.Fatal("expected an error for an empty name")
	}
	q, err := svc.SaveQuery(SavedQuery{Name: " Top customers ", SQL: "SELECT 1", ConnectionID: "c1"})
	if err != nil || q.ID == "" || q.Name != "Top customers" || q.Updated == "" {
		t.Fatalf("save: %+v %v", q, err)
	}
	q.SQL = "SELECT 2"
	if _, err := svc.SaveQuery(q); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSavedQueries()
	if err != nil || len(list) != 1 || list[0].SQL != "SELECT 2" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := svc.DeleteSavedQuery(q.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.ListSavedQueries(); len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestIndexes(t *testing.T) {
	svc, id := newSQLite(t)
	if _, err := svc.ExecuteQuery(id, `CREATE TABLE a (id INTEGER, n TEXT, m TEXT UNIQUE, PRIMARY KEY (id, n)); CREATE INDEX a_nm ON a(n, m);`); err != nil {
		t.Fatal(err)
	}
	list, err := svc.GetIndexes(id, "", "a")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]driver.Index{}
	for _, ix := range list {
		byName[ix.Name] = ix
	}
	nm, ok := byName["a_nm"]
	if !ok || strings.Join(nm.Columns, ",") != "n,m" || nm.Unique || nm.Primary || !strings.Contains(nm.Definition, "CREATE INDEX") {
		t.Fatalf("a_nm: %+v (all: %+v)", nm, list)
	}
	var pk, uq int
	for _, ix := range list {
		if ix.Primary {
			pk++
		} else if ix.Unique {
			uq++
		}
	}
	if pk != 1 || uq != 1 || len(list) != 3 {
		t.Fatalf("indexes: %+v", list)
	}
}
