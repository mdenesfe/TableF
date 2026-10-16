package vertica

import (
	"net/url"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
)

func TestDSN(t *testing.T) {
	cfg := driver.Config{Host: "vx.local", User: "dbadmin", Password: "p@ss/w:rd", Database: "VMart"}
	u, err := url.Parse(dsn(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "vertica" || u.Host != "vx.local:5433" || u.Path != "/VMart" || u.User.Username() != "dbadmin" {
		t.Errorf("url = %s", u.Redacted())
	}
	if p, _ := u.User.Password(); p != cfg.Password {
		t.Error("password not round-tripped")
	}
	if u.Query().Get("tlsmode") != "none" {
		t.Errorf("tlsmode = %q", u.Query().Get("tlsmode"))
	}
	for mode, want := range map[string]string{"require": "server", "verify-full": "server-strict", "prefer": "prefer", "disable": "none"} {
		cfg.SSLMode = mode
		if got := tlsMode(cfg); got != want {
			t.Errorf("tlsMode(%s) = %s, want %s", mode, got, want)
		}
	}
	cfg.Options = map[string]string{"tlsmode": "prefer"}
	if u, _ := url.Parse(dsn(cfg)); u.Query().Get("tlsmode") != "prefer" {
		t.Error("tlsmode option ignored")
	}
}

func TestDialect(t *testing.T) {
	d := vertica{}
	if got := driver.QualifiedName(d, "public", `a"b`); got != `"public"."a""b"` {
		t.Errorf("QualifiedName = %q", got)
	}
	if d.Placeholder(2) != "?" || d.DefaultSchema(driver.Config{}) != "public" {
		t.Error("placeholder/default schema")
	}
	if q, skip := driver.Paginate(d, `SELECT * FROM "t"`, 10, 30); q != `SELECT * FROM "t" LIMIT 10 OFFSET 30` || skip != 0 {
		t.Errorf("Paginate = %q", q)
	}
}
