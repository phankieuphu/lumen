package mysql

import (
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
)

func TestMySQLConfig(t *testing.T) {
	mc, err := mysqlConfig(driver.Config{
		Host: "db", User: "u", Password: "p", Database: "d", ReadOnly: true, TLS: driver.TLSVerifyFull,
	}.WithDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if mc.Addr != "db:3306" || mc.MultiStatements || mc.InterpolateParams || mc.AllowAllFiles {
		t.Fatalf("unsafe or wrong config: %+v", mc)
	}
	if mc.TLSConfig != "true" {
		t.Fatalf("TLSConfig = %q", mc.TLSConfig)
	}
	if mc.Params["transaction_read_only"] != "1" {
		t.Fatal("read-only not set")
	}
	if _, err := mysqlConfig(driver.Config{TLS: "nope"}); err == nil {
		t.Fatal("expected error for unknown TLS mode")
	}
}
