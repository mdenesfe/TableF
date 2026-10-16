package mssql

import (
	"net/url"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
	"github.com/microsoft/go-mssqldb/msdsn"
)

func TestPaginate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT * FROM [dbo].[t]", "SELECT * FROM [dbo].[t] ORDER BY (SELECT NULL) OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY"},
		{"SELECT * FROM [dbo].[t] ORDER BY [id] ASC", "SELECT * FROM [dbo].[t] ORDER BY [id] ASC OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY"},
		{"SELECT * FROM [dbo].[t] order\n  by [id]", "SELECT * FROM [dbo].[t] order\n  by [id] OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY"},
		// ORDER BY inside a subquery, a string or a bracketed name doesn't count.
		{"SELECT * FROM [dbo].[t] WHERE ([x] IN (SELECT TOP 1 x FROM y ORDER BY x))", "SELECT * FROM [dbo].[t] WHERE ([x] IN (SELECT TOP 1 x FROM y ORDER BY x)) ORDER BY (SELECT NULL) OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY"},
		{"SELECT * FROM [order by] WHERE (a = 'order by')", "SELECT * FROM [order by] WHERE (a = 'order by') ORDER BY (SELECT NULL) OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY"},
		{"SELECT * FROM [t] WHERE (border = 1)", "SELECT * FROM [t] WHERE (border = 1) ORDER BY (SELECT NULL) OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY"},
	}
	for _, c := range cases {
		got, skip := mssql{}.Paginate(c.in, 10, 20)
		if got != c.want || skip != 0 {
			t.Errorf("Paginate(%q)\n got %q, %d\nwant %q", c.in, got, skip, c.want)
		}
	}
}

func TestQuoteAndPlaceholder(t *testing.T) {
	d := mssql{}
	if got := d.QuoteIdent("we]ird"); got != "[we]]ird]" {
		t.Errorf("QuoteIdent = %q", got)
	}
	if got := d.Placeholder(3); got != "@p3" {
		t.Errorf("Placeholder = %q", got)
	}
	if got := driver.QualifiedName(d, "dbo", "t"); got != "[dbo].[t]" {
		t.Errorf("QualifiedName = %q", got)
	}
	if got := d.EmptyInsert("[dbo].[t]"); got != "INSERT INTO [dbo].[t] DEFAULT VALUES" {
		t.Errorf("EmptyInsert = %q", got)
	}
}

func TestDSN(t *testing.T) {
	cfg := driver.Config{Host: "db.local", User: "sa", Password: "p@ss:w/rd", Database: "app"}
	u, err := url.Parse(dsn(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "sqlserver" || u.Host != "db.local:1433" {
		t.Errorf("url = %s", u.Redacted())
	}
	if p, _ := u.User.Password(); p != cfg.Password || u.User.Username() != "sa" {
		t.Errorf("credentials not round-tripped")
	}
	q := u.Query()
	if q.Get("database") != "app" || q.Get("encrypt") != "false" || q.Get("TrustServerCertificate") != "true" {
		t.Errorf("query = %v", q)
	}

	cfg.SSLMode = "disable"
	if q := mustQuery(t, dsn(cfg)); q.Get("encrypt") != "disable" {
		t.Errorf("disable: %v", q)
	}
	cfg.SSLMode = "require"
	if q := mustQuery(t, dsn(cfg)); q.Get("encrypt") != "true" || q.Get("TrustServerCertificate") != "true" {
		t.Errorf("require: %v", q)
	}
	cfg.SSLMode = "verify-full"
	if q := mustQuery(t, dsn(cfg)); q.Get("encrypt") != "true" || q.Get("TrustServerCertificate") != "false" {
		t.Errorf("verify-full: %v", q)
	}
	cfg.Options = map[string]string{"encrypt": "disable", "trustServerCertificate": "true"}
	if q := mustQuery(t, dsn(cfg)); q.Get("encrypt") != "disable" {
		t.Errorf("option override: %v", q)
	}
}

func mustQuery(t *testing.T, s string) url.Values {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestDSNParsesWithDriver(t *testing.T) {
	cfg := driver.Config{Host: "db.local", Port: 1444, User: "sa", Password: "p@ss;w=rd", Database: "app"}
	c, err := msdsn.Parse(dsn(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "db.local" || c.Port != 1444 || c.Database != "app" || c.User != "sa" || c.Password != cfg.Password || c.AppName != "TableF" {
		t.Errorf("parsed config mismatch: host=%s port=%d db=%s app=%s", c.Host, c.Port, c.Database, c.AppName)
	}
	// Without an SSL mode the login packet (with the password) is still encrypted.
	if c.Encryption != msdsn.EncryptionOff {
		t.Errorf("encryption = %v", c.Encryption)
	}
}
