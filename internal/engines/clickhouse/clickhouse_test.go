package clickhouse

import (
	"context"
	sqldriver "database/sql/driver"
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
)

func TestRewriteUpdate(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"UPDATE `db`.`t` SET `a` = ?, `b` = ? WHERE `id` = ?", "ALTER TABLE `db`.`t` UPDATE `a` = ?, `b` = ? WHERE `id` = ?", true},
		{"update `db`.`set` set `offset` = ? where `id` = ?", "ALTER TABLE `db`.`set` UPDATE `offset` = ? where `id` = ?", true},
		{"UPDATE `my set`.`t` SET x = 1", "ALTER TABLE `my set`.`t` UPDATE x = 1", true},
		{"DELETE FROM `db`.`t` WHERE `id` = ?", "DELETE FROM `db`.`t` WHERE `id` = ?", false},
		{"INSERT INTO `db`.`t` (`a`) VALUES (?)", "INSERT INTO `db`.`t` (`a`) VALUES (?)", false},
		{"UPDATE nothing", "UPDATE nothing", false},
	}
	for _, c := range cases {
		got, ok := rewriteUpdate(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("rewriteUpdate(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestQuoting(t *testing.T) {
	d := clickhouse{}
	if got := driver.QualifiedName(d, "default", "we`ird\\"); got != "`default`.`we``ird\\\\`" {
		t.Errorf("QualifiedName = %q", got)
	}
	if d.Placeholder(4) != "?" {
		t.Error("placeholder")
	}
	if d.DefaultSchema(driver.Config{}) != "default" || d.DefaultSchema(driver.Config{Database: "logs"}) != "logs" {
		t.Error("DefaultSchema")
	}
	// The default LIMIT/OFFSET paging is used.
	if q, _ := driver.Paginate(d, "SELECT * FROM `t`", 10, 5); q != "SELECT * FROM `t` LIMIT 10 OFFSET 5" {
		t.Errorf("Paginate = %q", q)
	}
}

func TestIsNullable(t *testing.T) {
	for typ, want := range map[string]bool{
		"Nullable(String)": true, "LowCardinality(Nullable(String))": true,
		"String": false, "Array(Nullable(Int32))": false,
	} {
		if isNullable(typ) != want {
			t.Errorf("isNullable(%q) != %v", typ, want)
		}
	}
}

func TestOptions(t *testing.T) {
	o := options(driver.Config{Host: "ch.local", Password: "x"})
	if o.Addr[0] != "ch.local:9000" || o.TLS != nil || o.Auth.Database != "default" || o.Auth.Username != "default" {
		t.Errorf("defaults: addr=%v tls=%v db=%s user=%s", o.Addr, o.TLS != nil, o.Auth.Database, o.Auth.Username)
	}
	o = options(driver.Config{Host: "ch.local", User: "u", Database: "logs", Options: map[string]string{"secure": "true"}})
	if o.Addr[0] != "ch.local:9440" || o.TLS == nil || o.TLS.InsecureSkipVerify || o.Auth.Database != "logs" {
		t.Errorf("secure: addr=%v tls=%+v", o.Addr, o.TLS)
	}
	o = options(driver.Config{Host: "ch.local", Port: 9001, SSLMode: "require"})
	if o.Addr[0] != "ch.local:9001" || o.TLS == nil || !o.TLS.InsecureSkipVerify {
		t.Errorf("require: addr=%v", o.Addr)
	}
}

// fakeConn records the statement the wrapper forwards.
type fakeConn struct{ last string }

func (f *fakeConn) Prepare(string) (sqldriver.Stmt, error) { return nil, nil }
func (f *fakeConn) Close() error                           { return nil }
func (f *fakeConn) Begin() (sqldriver.Tx, error)           { return nil, nil }
func (f *fakeConn) ExecContext(_ context.Context, q string, _ []sqldriver.NamedValue) (sqldriver.Result, error) {
	f.last = q
	return sqldriver.RowsAffected(0), nil
}

func TestConnWrapper(t *testing.T) {
	f := &fakeConn{}
	c := &conn{f}
	r, err := c.ExecContext(context.Background(), "UPDATE `d`.`t` SET `a` = ? WHERE `id` = ?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.last != "ALTER TABLE `d`.`t` UPDATE `a` = ? WHERE `id` = ?" {
		t.Errorf("forwarded %q", f.last)
	}
	// The core only checks RowsAffected when it doesn't error.
	if _, err := r.RowsAffected(); err == nil {
		t.Error("RowsAffected should report unknown")
	}
}
