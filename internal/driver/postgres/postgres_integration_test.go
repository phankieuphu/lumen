//go:build integration

package postgres_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/driver/drivertest"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
)

func TestConformance(t *testing.T) {
	dsn := os.Getenv("LUMEN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LUMEN_TEST_POSTGRES_DSN not set")
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	drivertest.Run(t, drivertest.Engine{
		Dialect: driver.Postgres,
		Config: driver.Config{
			Host: pc.Host, Port: int(pc.Port), User: pc.User, Password: pc.Password,
			Database: pc.Database, TLS: driver.TLSDisable,
		},
		SleepSQL: "SELECT pg_sleep(30)",
		Table:    "lumen_conformance",
	})
}
