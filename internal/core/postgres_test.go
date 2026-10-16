package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Runs against a real server when TABLEF_TEST_PG_PORT is set, e.g.
//
//	docker run --rm -e POSTGRES_PASSWORD=tablef -p 127.0.0.1:55432:5432 postgres:17-alpine
//	TABLEF_TEST_PG_PORT=55432 go test ./internal/core/
func TestPostgresRoutines(t *testing.T) {
	port, _ := strconv.Atoi(os.Getenv("TABLEF_TEST_PG_PORT"))
	if port == 0 {
		t.Skip("TABLEF_TEST_PG_PORT not set")
	}
	svc := NewService(NewStore(filepath.Join(t.TempDir(), "c.json")))
	t.Cleanup(svc.Shutdown)
	cfg, err := svc.SaveConnection(driver.Config{
		Name: "pg", Driver: "postgres", Host: "127.0.0.1", Port: port,
		User: "postgres", Password: "tablef", Database: "postgres", SSLMode: "disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Connect(cfg.ID); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ExecuteQuery(cfg.ID, `
		DROP FUNCTION IF EXISTS add_one(int);
		DROP PROCEDURE IF EXISTS noop(text);
		CREATE FUNCTION add_one(x int) RETURNS int LANGUAGE sql AS $$ SELECT x + 1; $$;
		CREATE PROCEDURE noop(msg text) LANGUAGE plpgsql AS $$ BEGIN RAISE NOTICE '%', msg; END; $$;`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Error != "" {
			t.Fatalf("%s: %s", r.Statement, r.Error)
		}
	}

	routines, err := svc.ListRoutines(cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]driver.Routine{}
	for _, r := range routines {
		found[r.Name] = r
	}
	fn, ok := found["add_one"]
	if !ok || fn.Kind != "function" || fn.Arguments != "x integer" || fn.ReturnType != "integer" {
		t.Fatalf("add_one: %+v (all: %+v)", fn, routines)
	}
	if p, ok := found["noop"]; !ok || p.Kind != "procedure" {
		t.Fatalf("noop: %+v", p)
	}

	if err := svc.CreateSchema(cfg.ID, "tablef_test_schema"); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	schemas, err := svc.ListSchemas(cfg.ID)
	if err != nil || !strings.Contains(strings.Join(schemas, ","), "tablef_test_schema") {
		t.Fatalf("schemas: %v %v", schemas, err)
	}

	if _, err := svc.ExecuteQuery(cfg.ID, `DROP TABLE IF EXISTS grp_t; CREATE TABLE grp_t (id int, k text); INSERT INTO grp_t VALUES (1,'a'),(2,'a'),(3,NULL);`); err != nil {
		t.Fatal(err)
	}
	g, err := svc.GetGroups(cfg.ID, GroupRequest{Schema: "public", Table: "grp_t", Column: "k", Where: `"id" > 0`})
	if err != nil || g.Total != 2 || g.Groups[0].Count != 2 || g.Groups[1].Value != nil {
		t.Fatalf("pg groups: %+v %v", g, err)
	}
	rows, err := svc.GetTableData(cfg.ID, TableDataRequest{Schema: "public", Table: "grp_t", Match: []ColumnMatch{{"k", "a"}}})
	if err != nil || rows.Total != 2 {
		t.Fatalf("pg match: %+v %v", rows, err)
	}

	def, err := svc.GetRoutineDefinition(cfg.ID, fn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "CREATE OR REPLACE FUNCTION public.add_one(x integer)") {
		t.Fatalf("definition: %s", def)
	}
}
