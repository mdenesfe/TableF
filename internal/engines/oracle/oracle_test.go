package oracle

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
	goora "github.com/sijms/go-ora/v2"
)

func TestPaginate(t *testing.T) {
	got, skip := oracle{}.Paginate(`SELECT * FROM "HR"."EMP"`, 50, 100)
	want := `SELECT * FROM "HR"."EMP" OFFSET 100 ROWS FETCH NEXT 50 ROWS ONLY`
	if got != want || skip != 0 {
		t.Errorf("got %q, %d", got, skip)
	}
}

func TestQuoting(t *testing.T) {
	d := oracle{}
	if got := driver.QualifiedName(d, "HR", `we"ird`); got != `"HR"."we""ird"` {
		t.Errorf("QualifiedName = %q", got)
	}
	if got := d.Placeholder(2); got != ":2" {
		t.Errorf("Placeholder = %q", got)
	}
	if got := d.DefaultSchema(driver.Config{User: "scott"}); got != "SCOTT" {
		t.Errorf("DefaultSchema = %q", got)
	}
}

func TestSplitQualified(t *testing.T) {
	cases := map[string][]string{
		`"HR"."EMP"`:   {"HR", "EMP"},
		`"H""R"."E.M"`: {`H"R`, "E.M"},
		`"T"`:          {"T"},
		`HR.EMP`:       nil,
		`"HR"."EMP`:    nil,
		`"HR"x"EMP"`:   nil,
	}
	for in, want := range cases {
		if got := splitQualified(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitQualified(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmptyInsert(t *testing.T) {
	q := oracle{}.EmptyInsert(`"HR"."O'Brien"`)
	for _, want := range []string{
		`owner = 'HR' AND table_name = 'O''Brien'`,
		`EXECUTE IMMEDIATE 'INSERT INTO "HR"."O''Brien" ("' || REPLACE(c, '"', '""') || '") VALUES (DEFAULT)'`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("EmptyInsert missing %q in:\n%s", want, q)
		}
	}
	if q := (oracle{}).EmptyInsert("bad"); !strings.Contains(q, "RAISE_APPLICATION_ERROR") {
		t.Errorf("unparseable table should raise: %s", q)
	}
}

func TestDSN(t *testing.T) {
	cfg := driver.Config{Host: "ora.local", User: "scott", Password: "ti@g/er", Database: "FREEPDB1", SSLMode: "verify-full"}
	c, err := goora.ParseConfig(dsn(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseInfo.Servers[0].Addr != "ora.local" || c.DatabaseInfo.Servers[0].Port != 1521 {
		t.Errorf("server = %+v", c.DatabaseInfo.Servers[0])
	}
	if c.DatabaseInfo.ServiceName != "FREEPDB1" || c.UserID != "scott" || c.Password != cfg.Password {
		t.Errorf("service/credentials not round-tripped: service=%s user=%s", c.DatabaseInfo.ServiceName, c.UserID)
	}
	if !c.SSL || !c.SSLVerify {
		t.Errorf("ssl=%v verify=%v", c.SSL, c.SSLVerify)
	}

	cfg.SSLMode, cfg.Port = "", 1600
	cfg.Options = map[string]string{"sid": "ORCL"}
	c, err = goora.ParseConfig(dsn(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if c.SSL || c.DatabaseInfo.SID != "ORCL" || c.DatabaseInfo.Servers[0].Port != 1600 {
		t.Errorf("ssl=%v sid=%q port=%d", c.SSL, c.DatabaseInfo.SID, c.DatabaseInfo.Servers[0].Port)
	}
}
