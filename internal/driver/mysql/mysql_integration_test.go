//go:build integration

package mysql_test

import (
	"net"
	"os"
	"strconv"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/patrick-rontech/kiemdb/internal/driver"
	"github.com/patrick-rontech/kiemdb/internal/driver/drivertest"
	_ "github.com/patrick-rontech/kiemdb/internal/driver/mysql"
)

func TestConformance(t *testing.T) {
	dsn := os.Getenv("KIEMDB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("KIEMDB_TEST_MYSQL_DSN not set")
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
		Table:    "kiemdb_conformance",
	})
}
