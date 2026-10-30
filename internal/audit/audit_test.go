package audit

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrick-rontech/kiemdb/internal/store"
)

func newLog(t *testing.T) (*Log, *store.Store) {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "kiemdb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s), s
}

func entry(i int) Entry {
	return Entry{
		ConnID: "c1", ConnName: "local", Dialect: "postgres", Env: "dev", Kind: "write",
		Status: StatusOK, RowsAffected: int64(i), Duration: 12 * time.Millisecond,
		SQL: fmt.Sprintf("DELETE FROM t WHERE id = %d", i),
	}
}

func TestAppendChainsAndVerifies(t *testing.T) {
	l, _ := newLog(t)
	ctx := t.Context()
	var prev Entry
	for i := 1; i <= 5; i++ {
		e, err := l.Append(ctx, entry(i))
		if err != nil {
			t.Fatal(err)
		}
		if e.Seq != int64(i) {
			t.Fatalf("seq = %d, want %d", e.Seq, i)
		}
		if i > 1 && !bytes.Equal(e.PrevHash, prev.Hash) {
			t.Fatal("entry not linked to previous hash")
		}
		prev = e
	}
	res, err := l.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Entries != 5 {
		t.Fatalf("verify = %+v", res)
	}
	seq, h, err := l.Head(ctx)
	if err != nil || seq != 5 || !bytes.Equal(h, prev.Hash) {
		t.Fatalf("head = %d %x %v", seq, h, err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		tamper string
		want   string
	}{
		{"edited_sql", `UPDATE audit_log SET sql_text = 'SELECT 1' WHERE seq = 2`, "content"},
		{"deleted_middle", `DELETE FROM audit_log WHERE seq = 2`, "missing"},
		{"relinked", `UPDATE audit_log SET prev_hash = x'00' WHERE seq = 3`, "link"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, s := newLog(t)
			ctx := t.Context()
			for i := 1; i <= 3; i++ {
				if _, err := l.Append(ctx, entry(i)); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate an attacker with file access who drops the triggers.
			db := s.DB()
			for _, q := range []string{`DROP TRIGGER audit_log_no_update`, `DROP TRIGGER audit_log_no_delete`, tt.tamper} {
				if _, err := db.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			res, err := l.Verify(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if res.OK || !strings.Contains(res.Reason, tt.want) {
				t.Fatalf("verify = %+v, want failure mentioning %q", res, tt.want)
			}
		})
	}
}

func TestAppendRedactsAndTruncates(t *testing.T) {
	l, _ := newLog(t)
	e := entry(1)
	e.SQL = "ALTER USER app PASSWORD 'hunter2'"
	got, err := l.Append(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.SQL, "hunter2") {
		t.Fatalf("password stored: %q", got.SQL)
	}

	e.SQL = "INSERT INTO t VALUES ('" + strings.Repeat("é", MaxSQLBytes) + "')"
	got, err = l.Append(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.SQL) > MaxSQLBytes+64 || !strings.Contains(got.SQL, "[truncated") {
		t.Fatalf("not truncated: len %d", len(got.SQL))
	}
	if res, _ := l.Verify(t.Context()); !res.OK {
		t.Fatalf("verify after truncation = %+v", res)
	}
}

func TestConcurrentAppendsKeepChain(t *testing.T) {
	l, _ := newLog(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if _, err := l.Append(t.Context(), entry(i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	res, err := l.Verify(t.Context())
	if err != nil || !res.OK || res.Entries != 20 {
		t.Fatalf("verify = %+v, %v", res, err)
	}
}

func TestListPagesAndFilters(t *testing.T) {
	l, _ := newLog(t)
	ctx := t.Context()
	for i := 1; i <= 5; i++ {
		e := entry(i)
		if i%2 == 0 {
			e.ConnID = "c2"
		}
		if _, err := l.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	page, err := l.List(ctx, Filter{ConnID: "c1", Limit: 2})
	if err != nil || len(page) != 2 || page[0].Seq != 1 || page[1].Seq != 3 {
		t.Fatalf("page 1 = %+v, %v", page, err)
	}
	page, err = l.List(ctx, Filter{ConnID: "c1", Limit: 2, AfterSeq: page[1].Seq})
	if err != nil || len(page) != 1 || page[0].Seq != 5 {
		t.Fatalf("page 2 = %+v, %v", page, err)
	}
}

func TestExport(t *testing.T) {
	l, _ := newLog(t)
	ctx := t.Context()
	e := entry(1)
	e.SQL = "=HYPERLINK(\"x\")"
	for _, x := range []Entry{e, entry(2)} {
		if _, err := l.Append(ctx, x); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if err := l.Export(ctx, &buf, JSON, Filter{}); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 2 || got[1]["seq"] != float64(2) || len(got[0]["hash"].(string)) != 64 {
		t.Fatalf("json = %v", got)
	}

	buf.Reset()
	if err := l.Export(ctx, &buf, CSV, Filter{}); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || recs[0][0] != "seq" {
		t.Fatalf("csv = %v", recs)
	}
	if !strings.HasPrefix(recs[1][10], "'=") {
		t.Fatalf("formula not defused: %q", recs[1][10])
	}

	// Empty export is still valid JSON.
	empty, _ := newLog(t)
	buf.Reset()
	if err := empty.Export(ctx, &buf, JSON, Filter{}); err != nil || strings.TrimSpace(buf.String()) != "[]" {
		t.Fatalf("empty export = %q, %v", buf.String(), err)
	}
}

func BenchmarkAppend(b *testing.B) {
	s, err := store.Open(b.Context(), filepath.Join(b.TempDir(), "kiemdb.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	l := New(s)
	e := entry(1)
	for b.Loop() {
		if _, err := l.Append(b.Context(), e); err != nil {
			b.Fatal(err)
		}
	}
}
