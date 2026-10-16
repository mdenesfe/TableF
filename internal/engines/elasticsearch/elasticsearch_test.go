package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
)

type fakeES struct {
	mu      sync.Mutex
	queries []map[string]any
	closed  []string
	auth    []string
}

var limitRe = regexp.MustCompile(`LIMIT (\d+)$`)

// 25 docs: id 1..25, name "n<id>", tag a|b|null.
func doc(i int) []any {
	var tag any
	switch i % 3 {
	case 0:
		tag = "a"
	case 1:
		tag = "b"
	}
	return []any{i, "n" + strconv.Itoa(i), tag, 1.5}
}

func (f *fakeES) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			write(map[string]any{"cluster_name": "test-cluster", "version": map[string]any{"number": "8.15.0"}})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/_sql/close" {
			f.closed = append(f.closed, body["cursor"].(string))
			write(map[string]any{"succeeded": true})
			return
		}
		if r.URL.Path != "/_sql" || r.URL.Query().Get("format") != "json" {
			w.WriteHeader(404)
			return
		}
		f.queries = append(f.queries, body)
		cols := []map[string]string{{"name": "id", "type": "long"}, {"name": "name", "type": "text"}, {"name": "tag", "type": "keyword"}, {"name": "score", "type": "float"}}
		page := func(from, to, fetch int, withCols bool) {
			if to > 25 {
				to = 25
			}
			end := min(from+fetch, to)
			rows := [][]any{}
			for i := from; i < end; i++ {
				rows = append(rows, doc(i+1))
			}
			resp := map[string]any{"rows": rows}
			if withCols {
				resp["columns"] = cols
			}
			if end < to {
				resp["cursor"] = fmt.Sprintf("c:%d:%d:%d", end, to, fetch)
			}
			write(resp)
		}
		if c, ok := body["cursor"].(string); ok && c == "g" {
			write(map[string]any{"rows": [][]any{{8}}})
			return
		} else if ok {
			var from, to, fetch int
			fmt.Sscanf(c, "c:%d:%d:%d", &from, &to, &fetch)
			page(from, to, fetch, false)
			return
		}
		q := body["query"].(string)
		fetch := int(body["fetch_size"].(float64))
		switch {
		case q == "SHOW TABLES":
			write(map[string]any{
				"columns": []map[string]string{{"name": "catalog", "type": "keyword"}, {"name": "name", "type": "keyword"}, {"name": "type", "type": "keyword"}, {"name": "kind", "type": "keyword"}},
				"rows":    [][]any{{"c", ".kibana", "TABLE", "INDEX"}, {"c", "docs", "TABLE", "INDEX"}, {"c", "docs-alias", "VIEW", "ALIAS"}},
			})
		case strings.HasPrefix(q, `SELECT * FROM "docs"`):
			to := 25
			if m := limitRe.FindStringSubmatch(q); m != nil {
				to, _ = strconv.Atoi(m[1])
			}
			page(0, to, fetch, true)
		case strings.HasPrefix(q, `SELECT COUNT(*) FROM "docs"`):
			write(map[string]any{"columns": []map[string]string{{"name": "COUNT(*)", "type": "long"}}, "rows": [][]any{{25}}})
		case strings.HasPrefix(q, `SELECT COUNT(*) AS n FROM "docs" GROUP BY "tag"`) || strings.HasPrefix(q, `SELECT "tag", COUNT(*) AS n FROM "docs"`):
			if strings.Contains(q, "CASE") || strings.Contains(q, ") g") {
				w.WriteHeader(400)
				write(map[string]any{"error": map[string]any{"type": "verification_exception", "reason": "unsupported"}, "status": 400})
				return
			}
			rows := [][]any{{"a", 8}, {"b", 9}, {nil, 8}}
			if strings.HasPrefix(q, "SELECT COUNT(*) AS n") {
				rows = [][]any{{8}, {9}}
				// last bucket arrives through a cursor page
				{
					write(map[string]any{"columns": []map[string]string{{"name": "n", "type": "long"}}, "rows": rows, "cursor": "g"})
					return
				}
			}
			write(map[string]any{"columns": []map[string]string{{"name": "tag", "type": "keyword"}, {"name": "n", "type": "long"}}, "rows": rows})
		default:
			w.WriteHeader(400)
			write(map[string]any{"error": map[string]any{"root_cause": []any{map[string]any{"type": "parsing_exception", "reason": "line 1:8: mismatched input"}}, "type": "parsing_exception", "reason": "outer"}, "status": 400})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cfgFor(srv *httptest.Server) driver.Config {
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	p, _ := strconv.Atoi(port)
	return driver.Config{Host: host, Port: p, Options: map[string]string{"fetchSize": "7"}}
}

func TestElasticsearch(t *testing.T) {
	f := &fakeES{}
	srv := f.server(t)
	d, err := driver.Get("elasticsearch")
	if err != nil {
		t.Fatal(err)
	}
	cfg := cfgFor(srv)
	cfg.User, cfg.Password = "elastic", "pw"
	db, err := d.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if v := d.(driver.Versioner).Version(ctx, db); v != "Elasticsearch 8.15.0" {
		t.Fatalf("version %q", v)
	}
	if dbs, err := d.Databases(ctx, db); err != nil || len(dbs) != 1 || dbs[0] != "test-cluster" {
		t.Fatalf("dbs %v %v", dbs, err)
	}

	tables, err := d.Tables(ctx, db)
	if err != nil || len(tables) != 2 || tables[0] != (driver.Table{Name: "docs", Type: "table"}) || tables[1] != (driver.Table{Name: "docs-alias", Type: "view"}) {
		t.Fatalf("tables %+v %v", tables, err)
	}
	cols, err := d.Columns(ctx, db, d.DefaultSchema(cfg), "docs")
	if err != nil || len(cols) != 4 || cols[0].Name != "id" || cols[0].Type != "LONG" || cols[3].Name != "score" {
		t.Fatalf("cols %+v %v", cols, err)
	}

	// The core's page 2 of 10: LIMIT 20, read through cursor pages of 7, skip 10.
	from := driver.QualifiedName(d, d.DefaultSchema(cfg), "docs")
	if from != `"docs"` {
		t.Fatal(from)
	}
	q, skip := driver.Paginate(d, "SELECT * FROM "+from+` WHERE "tag" = ?`, 10, 10)
	if q != `SELECT * FROM "docs" WHERE "tag" = ? LIMIT 20` || skip != 10 {
		t.Fatalf("paginate %q %d", q, skip)
	}
	rows, err := db.QueryContext(ctx, q, "a")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var name string
		var tag, score any
		if err := rows.Scan(&id, &name, &tag, &score); err != nil {
			t.Fatal(err)
		}
		if _, ok := score.(float64); !ok {
			t.Fatalf("score %T", score)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	ids = ids[skip:]
	if len(ids) != 10 || ids[0] != 11 || ids[9] != 20 {
		t.Fatalf("ids %v", ids)
	}
	f.mu.Lock()
	last := f.queries[len(f.queries)-1]
	cursorCalls := 0
	for _, q := range f.queries {
		if _, ok := q["cursor"]; ok {
			cursorCalls++
		}
	}
	f.mu.Unlock()
	_ = last
	if cursorCalls != 2 { // 7 + 7 + 6
		t.Fatalf("cursor calls %d", cursorCalls)
	}
	// Params went into the request.
	f.mu.Lock()
	var paramsSeen bool
	for _, q := range f.queries {
		if p, ok := q["params"].([]any); ok && len(p) == 1 && p[0] == "a" {
			paramsSeen = true
		}
	}
	nClosed := len(f.closed)
	f.mu.Unlock()
	if !paramsSeen {
		t.Fatal("params not sent")
	}
	if nClosed != 0 {
		t.Fatalf("finished cursor closed: %d", nClosed)
	}

	// Abandoned cursor gets closed.
	rows, err = db.QueryContext(ctx, "SELECT * FROM "+from)
	if err != nil {
		t.Fatal(err)
	}
	rows.Next()
	rows.Close()
	f.mu.Lock()
	if len(f.closed) != 1 || f.closed[0] != "c:7:25:7" {
		t.Fatalf("closed %v", f.closed)
	}
	f.mu.Unlock()

	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+from).Scan(&total); err != nil || total != 25 {
		t.Fatalf("total %d %v", total, err)
	}

	// The core's grouping queries.
	col := d.QuoteIdent("tag")
	gq, _ := driver.Paginate(d, fmt.Sprintf("SELECT %s, COUNT(*) AS n FROM %s%s GROUP BY %s ORDER BY CASE WHEN %s IS NULL THEN 1 ELSE 0 END, n DESC, %s", col, from, "", col, col, col), 500, 0)
	grows, err := db.QueryContext(ctx, gq)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for grows.Next() {
		n++
	}
	grows.Close()
	if n != 3 {
		t.Fatalf("groups %d", n)
	}
	var groups, sum int64
	err = db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*), COALESCE(SUM(n), 0) FROM (SELECT COUNT(*) AS n FROM %s%s GROUP BY %s) g", from, "", col)).Scan(&groups, &sum)
	if err != nil || groups != 3 || sum != 25 {
		t.Fatalf("group totals %d %d %v", groups, sum, err)
	}

	// Errors carry the root cause.
	_, err = db.QueryContext(ctx, "SELEC nonsense")
	if err == nil || !strings.Contains(err.Error(), "parsing_exception: line 1:8") {
		t.Fatalf("err %v", err)
	}
	if _, err := db.BeginTx(ctx, nil); err == nil {
		t.Fatal("tx should fail")
	}

	f.mu.Lock()
	for _, a := range f.auth {
		if !strings.HasPrefix(a, "Basic ") {
			t.Fatalf("auth %q", a)
		}
	}
	f.mu.Unlock()
}

func TestAPIKeyAndURL(t *testing.T) {
	f := &fakeES{}
	srv := f.server(t)
	cfg := cfgFor(srv)
	cfg.User = "ignored"
	cfg.Options["apiKey"] = "k3y"
	db, err := esDriver{}.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	if f.auth[0] != "ApiKey k3y" {
		t.Fatalf("auth %q", f.auth[0])
	}

	for _, c := range []struct {
		cfg  driver.Config
		want string
	}{
		{driver.Config{}, "http://localhost:9200"},
		{driver.Config{Host: "es", Port: 9243, Options: map[string]string{"scheme": "https"}}, "https://es:9243"},
		{driver.Config{Host: "https://cloud.es.io:443"}, "https://cloud.es.io:443"},
		{driver.Config{Host: "https://cloud.es.io"}, "https://cloud.es.io:9200"},
	} {
		got, err := baseURL(c.cfg)
		if err != nil || got != c.want {
			t.Errorf("baseURL(%+v) = %q %v, want %q", c.cfg, got, err, c.want)
		}
	}
	if _, err := baseURL(driver.Config{Options: map[string]string{"scheme": "ftp"}}); err == nil {
		t.Fatal("want scheme error")
	}
	f2 := driver.FeaturesOf(esDriver{})
	if f2.Edit || f2.Transactions || f2.Schemas || !f2.Groups || f2.Family != "elasticsearch" {
		t.Fatalf("features %+v", f2)
	}
}
