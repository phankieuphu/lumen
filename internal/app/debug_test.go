package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/phankieuphu/lumen/internal/debug"
	"github.com/phankieuphu/lumen/internal/query"
)

func TestDebugOnSQLite(t *testing.T) {
	ctx := t.Context()
	a := openTestApp(t)
	db := filepath.Join(t.TempDir(), "data.db")
	pw := "unused"
	mk := func(name, env string, readOnly bool) string {
		t.Helper()
		c, err := a.CreateConnection(ctx, ConnectionInput{Name: name, Dialect: "sqlite", Database: db, Env: env, ReadOnly: readOnly, Password: &pw})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	dev, prod, ro := mk("dev", "dev", false), mk("prod", "prod", false), mk("ro", "dev", true)
	for i, q := range []string{"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)", "INSERT INTO t VALUES (1, 'a'), (2, 'b'), (3, 'c')"} {
		if _, err := a.RunQuery(ctx, RunInput{RequestID: fmt.Sprint("r", i), ConnID: dev, SQL: q}); err != nil {
			t.Fatal(err)
		}
	}
	const script = "/* é */ DELETE FROM t WHERE id > 1; SELECT * FROM t"

	steps := a.DebugSteps("sqlite", script)
	if len(steps) != 2 || steps[0].Start != 8 || steps[1].Start != 36 || !steps[0].Undoable || steps[0].Kind != "write" {
		t.Fatalf("steps = %+v", steps) // offsets count é as one UTF-16 unit
	}

	if _, err := a.DryRun(ctx, DryRunInput{RequestID: "d0", ConnID: prod, SQL: script}); !errors.Is(err, query.ErrConfirmRequired) {
		t.Fatalf("production without confirmation: %v", err)
	}
	if _, err := a.DryRun(ctx, DryRunInput{RequestID: "d1", ConnID: ro, SQL: script}); !errors.Is(err, query.ErrReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
	res, err := a.DryRun(ctx, DryRunInput{RequestID: "d2", ConnID: prod, SQL: script, ConfirmWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.RolledBack || res.Statements[0].RowsAffected != 2 || res.Statements[1].RowCount != 1 || res.Statements[1].Start != 36 {
		t.Fatalf("dry run = %+v", res)
	}
	if s, _ := a.RunQuery(ctx, RunInput{RequestID: "count", ConnID: dev, SQL: "SELECT * FROM t"}); s.RowCount != 3 {
		t.Fatalf("the dry run kept its delete: %d rows left", s.RowCount)
	}
	entries, err := a.AuditList(ctx, AuditFilter{ConnID: prod})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != "rolledback" || entries[0].TxLabel != "dry run" || entries[0].RowsAffected != 2 {
		t.Fatalf("audit = %+v", entries)
	}
	if v, err := a.AuditVerify(ctx); err != nil || !v.OK {
		t.Fatalf("audit chain: %+v %v", v, err)
	}

	rep, err := a.DebugParts(ctx, dev, "SELECT * FROM t WHERE name <> 'é' AND id > 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Parts) != 3 || rep.Parts[1].Rows != 2 || rep.Parts[1].End != 44 {
		t.Fatalf("parts = %+v", rep.Parts)
	}
	if blocked, err := a.AuditList(ctx, AuditFilter{ConnID: ro}); err != nil || len(blocked) != 1 || blocked[0].Status != "blocked" {
		t.Fatalf("read-only refusal audit = %+v, %v", blocked, err)
	}
	if _, err := a.DryRun(ctx, DryRunInput{RequestID: "d3", ConnID: dev, SQL: "DELETE FROM t; COMMIT"}); !errors.Is(err, debug.ErrNotUndoable) {
		t.Fatalf("COMMIT in a dry run: %v", err)
	}
	if _, err := a.DebugParts(ctx, dev, "DELETE FROM t"); !errors.Is(err, debug.ErrStatement) {
		t.Fatalf("parts of a delete: %v", err)
	}
	if err := a.CancelDebug(ctx, "nothing"); !errors.Is(err, query.ErrNotRunning) {
		t.Fatalf("cancel: %v", err)
	}
}
