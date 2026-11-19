package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/phankieuphu/lumen/internal/advisor"
	"github.com/phankieuphu/lumen/internal/lint"
	"github.com/phankieuphu/lumen/internal/schema"
	"github.com/phankieuphu/lumen/internal/secret"
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

	sch, err := a.Schema(ctx, c.ID)
	if err != nil || len(sch.Tables) != 1 || sch.Tables[0].Name != "t" || len(sch.Tables[0].Columns) != 2 {
		t.Fatalf("schema = %+v, %v", sch, err)
	}
	if _, err := a.Schema(ctx, "missing"); err == nil {
		t.Fatal("expected error for unknown connection")
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

// TestAdvisorOnSQLite checks the advisor is wired through the façade and
// says plainly that SQLite keeps no statement statistics.
func TestAdvisorOnSQLite(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	a, err := Open(ctx, Config{DataDir: dir, Version: "test", Secrets: &secret.Memory{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	c, err := a.CreateConnection(ctx, ConnectionInput{Name: "local", Dialect: "sqlite", Database: filepath.Join(dir, "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AdvisorList(ctx, c.ID, advisor.Filter{}); !errors.Is(err, advisor.ErrUnsupported) {
		t.Fatalf("list err = %v, want ErrUnsupported", err)
	}
	if _, err := a.AdvisorDetail(ctx, c.ID, "x"); !errors.Is(err, advisor.ErrUnsupported) {
		t.Fatalf("detail err = %v, want ErrUnsupported", err)
	}
	if _, err := a.AdvisorList(ctx, "no-such-connection", advisor.Filter{}); err == nil {
		t.Fatal("want an error for an unknown connection")
	}
}

func TestTransactionsThroughApp(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	a, err := Open(ctx, Config{DataDir: dir, Version: "test", Secrets: &secret.Memory{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	c, err := a.CreateConnection(ctx, ConnectionInput{Name: "local", Dialect: "sqlite", Database: filepath.Join(dir, "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunQuery(ctx, RunInput{RequestID: "c", ConnID: c.ID, SQL: "CREATE TABLE t (id INTEGER)"}); err != nil {
		t.Fatal(err)
	}
	tx, err := a.BeginTx(ctx, c.ID, " cleanup ")
	if err != nil || tx.Label != "cleanup" || tx.StartedAt == "" {
		t.Fatalf("begin = %+v, %v", tx, err)
	}
	sum, err := a.RunQuery(ctx, RunInput{RequestID: "i", ConnID: c.ID, SQL: "INSERT INTO t VALUES (1)", TxID: tx.ID})
	if err != nil || sum.TxID != tx.ID || sum.TxLabel != "cleanup" {
		t.Fatalf("insert = %+v, %v", sum, err)
	}
	open, _ := a.Transactions(ctx)
	if len(open) != 1 || open[0].Writes != 1 {
		t.Fatalf("open = %+v", open)
	}
	hs, err := a.TransactionHistory(ctx, tx.ID)
	if err != nil || len(hs) != 2 || hs[1].TxID != tx.ID {
		t.Fatalf("tx history = %+v, %v", hs, err)
	}
	// Deleting the connection rolls back first; its pool could not close
	// while the transaction holds a session.
	if err := a.DeleteConnection(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if open, _ := a.Transactions(ctx); len(open) != 0 {
		t.Fatalf("still open after delete: %+v", open)
	}
}

func TestEditorColors(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	a, err := Open(ctx, Config{DataDir: dir, Version: "test", Secrets: &secret.Memory{}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.EditorColors(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh colors = %v, %v; want empty", got, err)
	}
	want := map[string]string{"keyword": "#ff0000", "qualifier": "#00AA00"}
	if _, err := a.SaveEditorColors(ctx, want); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]map[string]string{
		"css injection": {"keyword": "red; background: url(x)"},
		"short hex":     {"keyword": "#fff"},
		"bad token":     {"key word": "#ffffff"},
	} {
		if _, err := a.SaveEditorColors(ctx, bad); err == nil {
			t.Errorf("%s: saved %v", name, bad)
		}
	}
	// Saved colours survive a restart, and rejected ones changed nothing.
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	a, err = Open(ctx, Config{DataDir: dir, Version: "test", Secrets: &secret.Memory{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	got, err := a.EditorColors(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("colors after restart = %v, %v; want %v", got, err, want)
	}
}

// schemaEvents counts EventSchemaChanged per connection.
type schemaEvents struct {
	mu sync.Mutex
	n  map[string]int
}

func (s *schemaEvents) Emit(event string, data any) {
	if event != EventSchemaChanged {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n[data.(SchemaChangedDTO).ConnID]++
}

func (s *schemaEvents) count(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n[id]
}

func TestSchemaCacheRefreshesAfterDDL(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	a := openTestApp(t)
	ev := &schemaEvents{n: map[string]int{}}
	a.SetEmitter(ev)
	pw := "unused"
	c, err := a.CreateConnection(ctx, ConnectionInput{
		Name: "local", Dialect: "sqlite", Database: filepath.Join(dir, "data.db"), Password: &pw,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := func(id, sql string) {
		t.Helper()
		if _, err := a.RunQuery(ctx, RunInput{RequestID: id, ConnID: c.ID, SQL: sql}); err != nil {
			t.Fatal(err)
		}
	}
	tables := func(s schema.Schema) int { return len(s.Tables) }

	s1, err := a.Schema(ctx, c.ID)
	if err != nil || tables(s1) != 0 {
		t.Fatalf("empty database schema = %+v, %v", s1, err)
	}
	run("r1", "SELECT 1")
	if s, _ := a.Schema(ctx, c.ID); s.Version != s1.Version || ev.count(c.ID) != 0 {
		t.Fatalf("a read changed the schema: version %d→%d, %d events", s1.Version, s.Version, ev.count(c.ID))
	}
	run("r2", "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)")
	if ev.count(c.ID) != 1 {
		t.Fatalf("DDL sent %d schema events, want 1", ev.count(c.ID))
	}
	s2, err := a.Schema(ctx, c.ID)
	if err != nil || tables(s2) != 1 || s2.Version <= s1.Version {
		t.Fatalf("after CREATE TABLE: %d tables, version %d (was %d), %v", tables(s2), s2.Version, s1.Version, err)
	}
	s3, err := a.RefreshSchema(ctx, c.ID)
	if err != nil || s3.Version <= s2.Version {
		t.Fatalf("RefreshSchema did not reload: version %d (was %d), %v", s3.Version, s2.Version, err)
	}
}

func TestLintUsesConnectionSettings(t *testing.T) {
	ctx := t.Context()
	a := openTestApp(t)
	pw := "unused"
	mk := func(name, env string) string {
		t.Helper()
		c, err := a.CreateConnection(ctx, ConnectionInput{Name: name, Dialect: "mysql", Host: "db.invalid", Database: "app", Env: env, Password: &pw})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	dev, prod := mk("dev", "dev"), mk("prod", "prod")
	sql := "DROP TABLE t; DELETE FROM 'é'"
	rules := func(id string) []string {
		t.Helper()
		got, err := a.Lint(ctx, id, sql)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(got))
		for _, f := range got {
			out = append(out, f.Rule)
		}
		return out
	}
	// The host does not exist: Lint must not connect.
	if got := fmt.Sprint(rules(prod)); got != "[L201]" {
		t.Errorf("prod rules = %s, want [L201]", got)
	}
	if got := rules(dev); len(got) != 0 {
		t.Errorf("dev rules = %v, want none", got)
	}
	if _, err := a.Lint(ctx, "no-such-connection", sql); err == nil {
		t.Error("Lint on an unknown connection did not fail")
	}
}

func TestLintIssueDTOsOverlap(t *testing.T) {
	// é is 2 bytes and 1 UTF-16 unit. The second finding starts inside the
	// first, so offsets go back.
	sql := "é SELECT x"
	got := lintIssueDTOs(sql, []lint.Finding{{Start: 3, End: 11, Rule: "A"}, {Start: 10, End: 11, Rule: "B"}})
	if got[0].From != 2 || got[0].To != 10 || got[1].From != 9 || got[1].To != 10 {
		t.Fatalf("got %+v", got)
	}
}
