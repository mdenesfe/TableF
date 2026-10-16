package snowflake

import (
	"testing"

	"github.com/mdenesfe/TableF/internal/driver"
	sf "github.com/snowflakedb/gosnowflake"
)

func TestDSN(t *testing.T) {
	if _, err := dsn(driver.Config{User: "u", Password: "p"}); err == nil {
		t.Error("missing account should fail")
	}
	cfg := driver.Config{
		User: "alice", Password: "p@ss/w:rd?", Database: "ANALYTICS",
		Options: map[string]string{"account": "xy12345.eu-central-1", "warehouse": "WH", "role": "ANALYST", "schema": "RAW"},
	}
	s, err := dsn(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sf.ParseDSN(s)
	if err != nil {
		t.Fatal(err)
	}
	if c.Account != "xy12345" || c.Region != "eu-central-1" || c.Host != "xy12345.eu-central-1.snowflakecomputing.com" {
		t.Errorf("account=%s region=%s host=%s", c.Account, c.Region, c.Host)
	}
	if c.User != "alice" || c.Password != cfg.Password || c.Database != "ANALYTICS" ||
		c.Warehouse != "WH" || c.Role != "ANALYST" || c.Schema != "RAW" || c.Application != "TableF" {
		t.Errorf("parsed config mismatch: user=%s db=%s wh=%s role=%s schema=%s app=%s",
			c.User, c.Database, c.Warehouse, c.Role, c.Schema, c.Application)
	}
}

func TestDialect(t *testing.T) {
	d := snowflake{}
	if got := driver.QualifiedName(d, "PUBLIC", `My"T`); got != `"PUBLIC"."My""T"` {
		t.Errorf("QualifiedName = %q", got)
	}
	if d.Placeholder(1) != "?" {
		t.Error("placeholder")
	}
	if d.DefaultSchema(driver.Config{}) != "PUBLIC" || d.DefaultSchema(driver.Config{Options: map[string]string{"schema": "RAW"}}) != "RAW" {
		t.Error("DefaultSchema")
	}
	if q, _ := driver.Paginate(d, `SELECT * FROM "T"`, 100, 0); q != `SELECT * FROM "T" LIMIT 100 OFFSET 0` {
		t.Errorf("Paginate = %q", q)
	}
}
