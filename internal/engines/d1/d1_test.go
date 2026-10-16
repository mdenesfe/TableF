package d1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
	_ "modernc.org/sqlite"
)

const token = "secret-token-123"

// fakeD1 answers the D1 /query API by running the SQL on a local SQLite file,
// writing result objects with keys in column order (like D1 does).
func fakeD1(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	back, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "d1.db"))
	if err != nil {
		t.Fatal(err)
	}
	back.SetMaxOpenConns(1)
	t.Cleanup(func() { back.Close() })
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/client/v4/accounts/acc/d1/database/db-1/query" || r.Method != http.MethodPost {
			http.Error(w, `{"success":false,"errors":[{"code":7404,"message":"not found"}]}`, 404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":[]}`))
			return
		}
		var req struct {
			SQL    string `json:"sql"`
			Params []any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		fail := func(msg string) {
			w.WriteHeader(400)
			b, _ := json.Marshal(map[string]any{"success": false, "errors": []any{map[string]any{"code": 7500, "message": msg}}, "result": []any{}})
			w.Write(b)
		}
		up := strings.ToUpper(strings.TrimSpace(req.SQL))
		var buf bytes.Buffer
		if strings.HasPrefix(up, "SELECT") || strings.HasPrefix(up, "PRAGMA") || strings.HasPrefix(up, "WITH") {
			rows, err := back.Query(req.SQL, req.Params...)
			if err != nil {
				fail(err.Error() + ": SQLITE_ERROR")
				return
			}
			cols, _ := rows.Columns()
			buf.WriteString(`{"success":true,"errors":[],"messages":[],"result":[{"results":[`)
			n := 0
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				rows.Scan(ptrs...)
				if n > 0 {
					buf.WriteByte(',')
				}
				buf.WriteByte('{')
				for i, c := range cols {
					if i > 0 {
						buf.WriteByte(',')
					}
					k, _ := json.Marshal(c)
					v := vals[i]
					if b, ok := v.([]byte); ok {
						ints := make([]int, len(b))
						for j := range b {
							ints[j] = int(b[j])
						}
						v = ints
					}
					vb, _ := json.Marshal(v)
					buf.Write(k)
					buf.WriteByte(':')
					buf.Write(vb)
				}
				buf.WriteByte('}')
				n++
			}
			rows.Close()
			fmt.Fprintf(&buf, `],"success":true,"meta":{"changes":0,"last_row_id":0,"rows_read":%d}}]}`, n)
		} else {
			res, err := back.Exec(req.SQL, req.Params...)
			if err != nil {
				fail(err.Error())
				return
			}
			ch, _ := res.RowsAffected()
			id, _ := res.LastInsertId()
			fmt.Fprintf(&buf, `{"success":true,"errors":[],"result":[{"results":[],"success":true,"meta":{"changes":%d,"last_row_id":%d}}]}`, ch, id)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func open(t *testing.T, srv *httptest.Server, tok string) *sql.DB {
	d, err := driver.Get("d1")
	if err != nil {
		t.Fatal(err)
	}
	db, err := d.Open(driver.Config{Options: map[string]string{
		"accountId": "acc", "databaseId": "db-1", "apiToken": tok, "apiBase": srv.URL + "/client/v4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestD1EndToEnd(t *testing.T) {
	srv, calls := fakeD1(t)
	db := open(t, srv, token)
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`CREATE TABLE users (zeta INTEGER PRIMARY KEY, alpha TEXT NOT NULL DEFAULT 'x', mid REAL, blob BLOB)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(zeta))`,
		`CREATE TABLE _cf_KV (k TEXT)`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	res, err := db.ExecContext(ctx, `INSERT INTO users (alpha, mid, blob) VALUES (?, ?, X'0102')`, "it's", 1.5)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 1 {
		t.Fatalf("last id %d", id)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("changes %d", n)
	}
	db.ExecContext(ctx, `INSERT INTO users (alpha, mid) VALUES ('b', NULL)`)

	// Column order must follow the SELECT, not alphabetical/map order.
	rows, err := db.QueryContext(ctx, `SELECT zeta, mid, alpha, blob FROM users WHERE zeta >= ? ORDER BY zeta`, 1)
	if err != nil {
		t.Fatal(err)
	}
	cols, _ := rows.Columns()
	if strings.Join(cols, ",") != "zeta,mid,alpha,blob" {
		t.Fatalf("columns %v", cols)
	}
	var got []string
	for rows.Next() {
		var a, b, c, e any
		if err := rows.Scan(&a, &b, &c, &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%T:%v|%T:%v|%v|%v", a, a, b, b, c, e))
	}
	rows.Close()
	if s := strings.Join(got, ";"); s != "int64:1|float64:1.5|it's|[1 2];int64:2|<nil>:<nil>|b|<nil>" {
		t.Fatalf("rows %s", s)
	}

	d, _ := driver.Get("d1")
	tables, err := d.Tables(ctx, db)
	if err != nil || len(tables) != 2 || tables[0].Name != "posts" || tables[1].Name != "users" {
		t.Fatalf("tables %+v %v", tables, err)
	}
	cs, err := d.Columns(ctx, db, "main", "users")
	if err != nil || len(cs) != 4 || !cs[0].PrimaryKey || cs[1].Nullable || *cs[1].Default != "'x'" || cs[2].Type != "REAL" {
		t.Fatalf("cols %+v %v", cs, err)
	}
	out, in, err := d.ForeignKeys(ctx, db, "main", "users")
	if err != nil || len(out) != 0 || len(in) != 1 || in[0].Table != "posts" || in[0].RefColumn != "zeta" {
		t.Fatalf("fks %+v %+v %v", out, in, err)
	}
	// The core's paged select and count on a qualified name.
	from := driver.QualifiedName(d, "main", "users")
	q, _ := driver.Paginate(d, "SELECT * FROM "+from+" ORDER BY \"zeta\"", 1, 1)
	var z int64
	var a string
	var m, bl any
	if err := db.QueryRowContext(ctx, q).Scan(&z, &a, &m, &bl); err != nil || z != 2 {
		t.Fatalf("page %d %v", z, err)
	}
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+from).Scan(&total); err != nil || total != 2 {
		t.Fatalf("total %d %v", total, err)
	}
	dbs, err := d.Databases(ctx, db)
	if err != nil || len(dbs) != 1 || dbs[0] != "db-1" {
		t.Fatalf("dbs %v %v", dbs, err)
	}
	if v := d.(driver.Versioner).Version(ctx, db); !strings.HasPrefix(v, "Cloudflare D1 (SQLite 3.") {
		t.Fatalf("version %q", v)
	}
	if _, err := db.BeginTx(ctx, nil); err == nil {
		t.Fatal("transactions should be refused")
	}
	// Errors come back with the API message.
	_, err = db.ExecContext(ctx, "INSERT INTO nope VALUES (1)")
	if err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("err %v", err)
	}
	if atomic.LoadInt32(calls) == 0 {
		t.Fatal("no calls")
	}
}

func TestD1AuthErrorHidesToken(t *testing.T) {
	srv, _ := fakeD1(t)
	db := open(t, srv, "wrong-"+token)
	err := db.Ping()
	if err == nil || !strings.Contains(err.Error(), "Authentication error") || strings.Contains(err.Error(), token) {
		t.Fatalf("err %v", err)
	}
}

func TestD1MissingOptions(t *testing.T) {
	if _, err := (d1Driver{}).Open(driver.Config{Options: map[string]string{"accountId": "a"}}); err == nil {
		t.Fatal("want error")
	}
}

func TestDecodeResults(t *testing.T) {
	// Later rows may add or reorder keys; values land in the right columns.
	r, err := decodeResults(json.RawMessage(`[{"b":1,"a":"x"},{"a":"y","b":2.5,"c":null},{"c":true}]`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.columns, ",") != "b,a,c" {
		t.Fatal(r.columns)
	}
	want := `[[1 x <nil>] [2.5 y <nil>] [<nil> <nil> true]]`
	if fmt.Sprint(r.rows) != want {
		t.Fatalf("%v", r.rows)
	}
	r, err = decodeResults(json.RawMessage(`{"columns":["z","a"],"rows":[[1,"q"]]}`))
	if err != nil || strings.Join(r.columns, ",") != "z,a" || fmt.Sprint(r.rows) != "[[1 q]]" {
		t.Fatalf("raw shape %v %v", r, err)
	}
	r, err = decodeResults(json.RawMessage(`[]`))
	if err != nil || len(r.columns) != 0 || len(r.rows) != 0 {
		t.Fatal(r, err)
	}
	if _, err := decodeResults(json.RawMessage(`"x"`)); err == nil {
		t.Fatal("want error")
	}
}

func TestSQLRegisteredDSN(t *testing.T) {
	srv, _ := fakeD1(t)
	b, _ := json.Marshal(dsn{AccountID: "acc", DatabaseID: "db-1", APIToken: token, APIBase: srv.URL + "/client/v4"})
	db, err := sql.Open(DriverName, string(b))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal(one, err)
	}
}
