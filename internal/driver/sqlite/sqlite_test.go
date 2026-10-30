package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/patrick-rontech/kiemdb/internal/driver"
	"github.com/patrick-rontech/kiemdb/internal/driver/drivertest"
	_ "github.com/patrick-rontech/kiemdb/internal/driver/sqlite"
)

// sleepSQL keeps SQLite busy for a long time so cancel can interrupt it.
const sleepSQL = `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c)
SELECT MAX(x) FROM c`

func TestConformance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conformance db?#.sqlite")
	drivertest.Run(t, drivertest.Engine{
		Dialect:  driver.SQLite,
		Config:   driver.Config{Database: path},
		SleepSQL: sleepSQL,
		Table:    "kiemdb_conformance",
	})
}

func TestGuardRejectsMultipleStatements(t *testing.T) {
	c, err := driver.Open(t.Context(), driver.SQLite, driver.Config{Database: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.Session(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Exec(t.Context(), "CREATE TABLE a(x); CREATE TABLE b(x)"); !errors.Is(err, driver.ErrMultipleStatements) {
		t.Fatalf("err = %v, want ErrMultipleStatements", err)
	}
	// A trigger body has inner semicolons but is one statement.
	for _, q := range []string{
		"CREATE TABLE t(x)",
		"CREATE TABLE log(x)",
		"CREATE TRIGGER tr AFTER INSERT ON t BEGIN INSERT INTO log VALUES (NEW.x); INSERT INTO log VALUES (0); END",
	} {
		if _, err := s.Exec(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := driver.Open(t.Context(), driver.SQLite, driver.Config{}); err == nil {
		t.Fatal("expected error for empty path")
	}
}
