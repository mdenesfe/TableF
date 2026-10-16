package libsql

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
	_ "modernc.org/sqlite"
)

func TestDSN(t *testing.T) {
	cases := []struct {
		url, token, want string
		err              bool
	}{
		{"libsql://db-org.turso.io", "tok", "libsql://db-org.turso.io?authToken=tok", false},
		{"db-org.turso.io", "", "libsql://db-org.turso.io", false},
		{"https://db.example.com", "a+b/c=", "https://db.example.com?authToken=a%2Bb%2Fc%3D", false},
		{"http://127.0.0.1:8080", "", "http://127.0.0.1:8080", false},
		{"libsql://h:8080?tls=0", "t", "libsql://h:8080?authToken=t&tls=0", false},
		{"https://h?authToken=old", "new", "https://h?authToken=new", false},
		{"", "", "", true},
		{"ftp://h", "", "", true},
	}
	for _, c := range cases {
		cfg := driver.Config{Options: map[string]string{"url": c.url, "authToken": c.token}}
		got, err := dsn(cfg)
		if c.err {
			if err == nil {
				t.Errorf("dsn(%q): want error, got %q", c.url, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("dsn(%q) = %q, %v; want %q", c.url, got, err, c.want)
		}
	}
}

func TestRegistered(t *testing.T) {
	d, err := driver.Get("libsql")
	if err != nil {
		t.Fatal(err)
	}
	f := driver.FeaturesOf(d)
	if f.Family != "sqlite" || f.Schemas || !f.Edit {
		t.Fatalf("features: %+v", f)
	}
	if q, _ := driver.Paginate(d, "SELECT 1", 10, 20); q != "SELECT 1 LIMIT 10 OFFSET 20" {
		t.Fatal(q)
	}
}

// The catalog queries are plain SQLite; run them through the libsql client's
// file: mode (backed by modernc sqlite) to check they parse and scan.
func TestCatalogOnLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	db, err := sql.Open("libsql", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT 'x')`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), editor INTEGER REFERENCES users)`,
		`CREATE TABLE _litestream_seq (id INTEGER)`,
		`CREATE VIEW v AS SELECT * FROM users`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	d := libsqlDriver{}
	tables, err := d.Tables(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name+":"+tb.Type)
	}
	if got := strings.Join(names, ","); got != "posts:table,users:table,v:view" {
		t.Fatalf("tables %s", got)
	}
	cols, err := d.Columns(ctx, db, "main", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || !cols[0].PrimaryKey || cols[1].Nullable || cols[1].Default == nil || *cols[1].Default != "'x'" {
		t.Fatalf("cols %+v", cols)
	}
	out, in, err := d.ForeignKeys(ctx, db, "main", "posts")
	if err != nil || len(out) != 2 || len(in) != 0 {
		t.Fatalf("posts fks out=%+v in=%+v err=%v", out, in, err)
	}
	for _, f := range out {
		if f.RefTable != "users" || f.RefColumn != "id" {
			t.Fatalf("fk %+v", f)
		}
	}
	out, in, err = d.ForeignKeys(ctx, db, "main", "users")
	if err != nil || len(out) != 0 || len(in) != 2 {
		t.Fatalf("users fks out=%+v in=%+v err=%v", out, in, err)
	}
	if v := d.Version(ctx, db); !strings.HasPrefix(v, "libSQL 3.") {
		t.Fatalf("version %q", v)
	}
}
