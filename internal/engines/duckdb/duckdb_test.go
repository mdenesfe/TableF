package duckdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
)

func TestDuckDBInMemory(t *testing.T) {
	d, err := driver.Get("duckdb")
	if err != nil {
		t.Fatal(err)
	}
	db, err := d.Open(driver.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(3) // several conns must still see one in-memory database
	ctx := context.Background()
	for _, s := range []string{
		`CREATE SCHEMA app`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name VARCHAR NOT NULL DEFAULT 'anon', age INTEGER)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), title VARCHAR)`,
		`CREATE TABLE app.things (a INTEGER, b INTEGER, PRIMARY KEY (a, b))`,
		`CREATE VIEW adults AS SELECT * FROM users WHERE age >= 18`,
		`INSERT INTO users SELECT i, 'u' || i, i FROM range(1, 26) t(i)`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	schemas, err := d.Schemas(ctx, db)
	if err != nil || strings.Join(schemas, ",") != "app,main" {
		t.Fatalf("schemas %v %v", schemas, err)
	}
	dbs, err := d.Databases(ctx, db)
	if err != nil || len(dbs) != 1 || dbs[0] != "memory" {
		t.Fatalf("databases %v %v", dbs, err)
	}
	if cur := d.(driver.CurrentDatabaser).CurrentDatabase(ctx, db); cur != "memory" {
		t.Fatalf("current %q", cur)
	}
	if v := d.(driver.Versioner).Version(ctx, db); !strings.HasPrefix(v, "DuckDB v") {
		t.Fatalf("version %q", v)
	}

	tables, err := d.Tables(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tb := range tables {
		got = append(got, tb.Schema+"."+tb.Name+":"+tb.Type)
	}
	if s := strings.Join(got, ","); s != "app.things:table,main.adults:view,main.posts:table,main.users:table" {
		t.Fatalf("tables %s", s)
	}

	cols, err := d.Columns(ctx, db, "main", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 3 || cols[0].Name != "id" || !cols[0].PrimaryKey || cols[0].Nullable ||
		cols[1].Nullable || cols[1].Default == nil || cols[2].PrimaryKey || !cols[2].Nullable || cols[1].Type != "VARCHAR" {
		t.Fatalf("cols %+v", cols)
	}
	tc, err := d.Columns(ctx, db, "app", "things")
	if err != nil || len(tc) != 2 || !tc[0].PrimaryKey || !tc[1].PrimaryKey {
		t.Fatalf("things cols %+v %v", tc, err)
	}

	out, in, err := d.ForeignKeys(ctx, db, "main", "posts")
	if err != nil || len(out) != 1 || len(in) != 0 || out[0].Column != "user_id" || out[0].RefTable != "users" || out[0].RefColumn != "id" || out[0].Name == "" {
		t.Fatalf("posts fks %+v %+v %v", out, in, err)
	}
	out, in, err = d.ForeignKeys(ctx, db, "", "users")
	if err != nil || len(out) != 0 || len(in) != 1 || in[0].Table != "posts" {
		t.Fatalf("users fks %+v %+v %v", out, in, err)
	}

	// Page through rows the way the core does, with ? placeholders.
	from := driver.QualifiedName(d, "main", "users")
	var seen []int
	for off := 0; off < 25; off += 10 {
		q, skip := driver.Paginate(d, fmt.Sprintf("SELECT * FROM %s WHERE age > %s ORDER BY id", from, d.Placeholder(1)), 10, off)
		if skip != 0 {
			t.Fatal("skip")
		}
		rows, err := db.QueryContext(ctx, q, 0)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int
			var name string
			var age any
			if err := rows.Scan(&id, &name, &age); err != nil {
				t.Fatal(err)
			}
			seen = append(seen, id)
		}
		rows.Close()
	}
	if len(seen) != 25 || seen[0] != 1 || seen[24] != 25 {
		t.Fatalf("paged %v", seen)
	}
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+from+" WHERE age > $1", 20).Scan(&n); err != nil || n != 5 {
		t.Fatalf("count %d %v", n, err)
	}

	// Edits in a transaction, as the core does.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE "+from+` SET "name" = ? WHERE "id" = ?`, "z", 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestDuckDBFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.duckdb")
	d := duckDriver{}
	db, err := d.Open(driver.Config{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE x (a INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if cur := d.CurrentDatabase(ctx, db); cur != "t" {
		t.Fatalf("current %q", cur)
	}
	db.Close()
	if got, err := dsn(driver.Config{FilePath: path, Options: map[string]string{"readOnly": "true"}}); err != nil || got != path+"?access_mode=READ_ONLY" {
		t.Fatal(got, err)
	}
	ro, err := d.Open(driver.Config{FilePath: path, Options: map[string]string{"readOnly": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	tables, err := d.Tables(ctx, ro)
	if err != nil || len(tables) != 1 || tables[0].Name != "x" {
		t.Fatalf("%v %v", tables, err)
	}
	if _, err := ro.ExecContext(ctx, "INSERT INTO x VALUES (1)"); err == nil {
		t.Fatal("read-only insert succeeded")
	}
}

// File names with characters that are special in URLs either open the right
// file or fail clearly; they must never open a different file.
func TestDuckDBOddPaths(t *testing.T) {
	d := duckDriver{}
	ctx := context.Background()
	for _, name := range []string{"a#b.duckdb", "50%41.duckdb", "sp ace.duckdb"} {
		path := filepath.Join(t.TempDir(), name)
		db, err := d.Open(driver.Config{FilePath: path})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, "CREATE TABLE x (a INTEGER)"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		db.Close()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: database not created at the given path: %v", name, err)
		}
	}
	for _, name := range []string{"a?b.duckdb", "100%.duckdb"} {
		if _, err := d.Open(driver.Config{FilePath: filepath.Join(t.TempDir(), name)}); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}
