//go:build integration

package mysql_test

import (
	"net"
	"os"
	"strconv"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/driver/drivertest"
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
)

func TestConformance(t *testing.T) {
	dsn := os.Getenv("LUMEN_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("LUMEN_TEST_MYSQL_DSN not set")
	}
	mc, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(mc.Addr)
	p, _ := strconv.Atoi(port)
	drivertest.Run(t, drivertest.Engine{
		Dialect: driver.MySQL,
		Config: driver.Config{
			Host: host, Port: p, User: mc.User, Password: mc.Passwd,
			Database: mc.DBName, TLS: driver.TLSDisable,
		},
		SleepSQL: "SELECT SLEEP(30)",
		Table:    "lumen_conformance",
	})
}
