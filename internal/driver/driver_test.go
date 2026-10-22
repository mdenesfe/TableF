package driver

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestQuoteIdent(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"postgres":    {`a"b`, `"a""b"`},
		"redshift":    {`a"b`, `"a""b"`},
		"cockroach":   {`a"b`, `"a""b"`},
		"greenplum":   {`a"b`, `"a""b"`},
		"sqlite":      {`we"ird`, `"we""ird"`},
		"mysql":       {"a`b", "`a``b`"},
		"mariadb":     {"a`b", "`a``b`"},
		"singlestore": {"a`b", "`a``b`"},
	}
	for name, c := range cases {
		d, err := Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := d.QuoteIdent(c.in); got != c.want {
			t.Errorf("%s: QuoteIdent(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestQualifiedNameAndPaginate(t *testing.T) {
	pg, _ := Get("postgres")
	if got := QualifiedName(pg, "s", "t"); got != `"s"."t"` {
		t.Errorf("QualifiedName = %q", got)
	}
	if got := QualifiedName(pg, "", "t"); got != `"t"` {
		t.Errorf("QualifiedName without schema = %q", got)
	}
	if q, skip := Paginate(pg, "SELECT 1", 10, 20); q != "SELECT 1 LIMIT 10 OFFSET 20" || skip != 0 {
		t.Errorf("Paginate = %q, %d", q, skip)
	}
	if pg.Placeholder(3) != "$3" {
		t.Error("postgres placeholder")
	}
}

func TestFeaturesAndHooks(t *testing.T) {
	my, _ := Get("mysql")
	maria, _ := Get("mariadb")
	if FeaturesOf(maria) != FeaturesOf(my) {
		t.Errorf("mariadb features %+v differ from mysql %+v", FeaturesOf(maria), FeaturesOf(my))
	}
	if _, ok := maria.(EmptyInserter); !ok {
		t.Error("mariadb lost EmptyInsert")
	}
	rs, _ := Get("redshift")
	if f := FeaturesOf(rs); f.Routines || f.ForeignKeys || !f.Schemas {
		t.Errorf("redshift features %+v", f)
	}
	var nr NoRoutines
	if r, err := nr.Routines(context.Background(), nil); err != nil || len(r) != 0 || r == nil {
		t.Errorf("NoRoutines = %v, %v", r, err)
	}
	if _, err := nr.RoutineDefinition(context.Background(), nil, Routine{}); err == nil {
		t.Error("NoRoutines.RoutineDefinition must fail")
	}
	out, in, err := NoForeignKeys{}.ForeignKeys(context.Background(), nil, "", "")
	if err != nil || out == nil || in == nil || len(out)+len(in) != 0 {
		t.Errorf("NoForeignKeys = %v %v %v", out, in, err)
	}
}

func TestConfigurePool(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pg, _ := Get("postgres")
	ConfigurePool(pg, db)
	if n := db.Stats().MaxOpenConnections; n != DefaultMaxOpenConns {
		t.Errorf("default pool: %d", n)
	}
	lite, _ := Get("sqlite")
	ConfigurePool(lite, db)
	if n := db.Stats().MaxOpenConnections; n != 1 {
		t.Errorf("sqlite pool: %d", n)
	}
}

func TestSQLitePathEscaping(t *testing.T) {
	d, _ := Get("sqlite")
	ctx := context.Background()
	for _, name := range []string{"a?b.db", "c#d.db", "50%.db", "e%41f.db", "sp ace.db"} {
		path := filepath.Join(t.TempDir(), name)
		db, err := d.Open(Config{FilePath: path})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, "CREATE TABLE x (a INTEGER)"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// The pragmas after '?' must still apply.
		var fk int
		if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Errorf("%s: foreign_keys = %d, %v", name, fk, err)
		}
		db.Close()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: database not created at its path: %v", name, err)
		}
	}
}

func TestSQLiteCatalog(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "c.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE parent (id INTEGER PRIMARY KEY, a INT, b INT, UNIQUE (a, b));
		CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INT REFERENCES parent(id), p2 INT REFERENCES parent,
			a INT, b INT, FOREIGN KEY (a, b) REFERENCES parent(a, b));
		CREATE TABLE _hidden_x (id INTEGER PRIMARY KEY, parent_id INT REFERENCES parent(id));
		CREATE VIEW v AS SELECT 1;`); err != nil {
		t.Fatal(err)
	}
	tables, err := SQLiteTables(ctx, db, []string{"sqlite_", "_hidden_"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name+":"+tb.Type)
	}
	if want := []string{"child:table", "parent:table", "v:view"}; !reflect.DeepEqual(names, want) {
		t.Errorf("tables %v, want %v", names, want)
	}

	out, in, err := SQLiteForeignKeys(ctx, db, "main", "child", []string{"_hidden_"})
	if err != nil {
		t.Fatal(err)
	}
	// The composite (a, b) key is left out; REFERENCES parent resolves to its key.
	if len(out) != 2 || len(in) != 0 {
		t.Fatalf("child: out %+v in %+v", out, in)
	}
	for _, f := range out {
		if f.RefTable != "parent" || f.RefColumn != "id" || (f.Column != "parent_id" && f.Column != "p2") {
			t.Errorf("unexpected key %+v", f)
		}
	}
	_, in, err = SQLiteForeignKeys(ctx, db, "main", "PARENT", []string{"_hidden_"})
	if err != nil || len(in) != 2 || in[0].Table != "child" {
		t.Fatalf("parent in: %+v %v", in, err)
	}
	cols, err := SQLiteColumns(ctx, db, "", "parent")
	if err != nil || len(cols) != 3 || !cols[0].PrimaryKey || cols[1].PrimaryKey || !cols[1].Nullable {
		t.Fatalf("columns %+v %v", cols, err)
	}
}

func TestMySQLTLS(t *testing.T) {
	for mode, want := range map[string]string{"": "", "disable": "", "prefer": "preferred", "require": "skip-verify", "verify-ca": "true", "verify-full": "true"} {
		if got := mysqlConfig(Config{Host: "h", SSLMode: mode}).TLSConfig; got != want {
			t.Errorf("sslMode %q: TLSConfig %q, want %q", mode, got, want)
		}
	}
}

func TestReadOnlyPostgresParams(t *testing.T) {
	if pgParams(Config{Driver: "postgres"}, "prefer").Has("default_transaction_read_only") {
		t.Error("a normal connection must not be read-only")
	}
	if pgParams(Config{Driver: "postgres", SafeMode: SafeModeReadOnly}, "prefer").Get("default_transaction_read_only") != "on" {
		t.Error("a read-only connection should ask the server for it")
	}
	if pgParams(Config{Driver: "redshift", SafeMode: SafeModeReadOnly}, "prefer").Has("default_transaction_read_only") {
		t.Error("Redshift doesn't know the setting")
	}
}
