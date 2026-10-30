package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/patrick-rontech/kiemdb/internal/secret"
)

type recorder struct {
	mu     sync.Mutex
	events []json.RawMessage
}

func (r *recorder) Emit(event string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event == EventQueryPage {
		r.events = append(r.events, data.(json.RawMessage))
	}
}

func TestEndToEnd(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	a, err := Open(ctx, Config{DataDir: dir, Version: "test", Secrets: &secret.Memory{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	rec := &recorder{}
	a.SetEmitter(rec)

	pw := "unused"
	c, err := a.CreateConnection(ctx, ConnectionInput{
		Name: "local", Dialect: "sqlite", Database: filepath.Join(dir, "data.db"), Password: &pw,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, q := range []string{"CREATE TABLE t (id INTEGER, name TEXT)", "INSERT INTO t VALUES (1, 'a'), (2, 'b')"} {
		if _, err := a.RunQuery(ctx, RunInput{RequestID: string(rune('a' + i)), ConnID: c.ID, SQL: q}); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := a.RunQuery(ctx, RunInput{RequestID: "sel", ConnID: c.ID, SQL: "SELECT * FROM t"})
	if err != nil || sum.RowCount != 2 || sum.Status != "ok" {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	if len(rec.events) != 1 || !strings.Contains(string(rec.events[0]), `"rows":[[1,"a"],[2,"b"]]`) {
		t.Fatalf("page events = %s", rec.events)
	}

	entries, err := a.AuditList(ctx, AuditFilter{})
	if err != nil || len(entries) != 2 {
		t.Fatalf("audit = %+v, %v", entries, err)
	}
	if res, err := a.AuditVerify(ctx); err != nil || !res.OK {
		t.Fatalf("verify = %+v, %v", res, err)
	}

	out := filepath.Join(dir, "audit.json")
	if err := a.AuditExport(ctx, out, "json", AuditFilter{}); err != nil {
		t.Fatal(err)
	}
	if err := a.AuditExport(ctx, out, "json", AuditFilter{}); err == nil {
		t.Fatal("export overwrote an existing file")
	}
	if err := a.AuditExport(ctx, filepath.Join(dir, "bad.out"), "xml", AuditFilter{}); err == nil {
		t.Fatal("expected error for unknown format")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.out")); !os.IsNotExist(err) {
		t.Fatal("failed export left a file behind")
	}

	hist, err := a.QueryHistory(ctx, c.ID, 0, 10)
	if err != nil || len(hist) != 3 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	list, err := a.ListConnections(ctx)
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("connections = %+v, %v", list, err)
	}
}
