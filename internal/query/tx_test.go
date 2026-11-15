package query

import (
	"errors"
	"strings"
	"testing"

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
)

func TestTxControl(t *testing.T) {
	tests := []struct {
		sql  string
		d    driver.Dialect
		want txOp
	}{
		{"BEGIN", driver.Postgres, txBegin},
		{"begin transaction isolation level serializable;", driver.Postgres, txBegin},
		{"BEGIN IMMEDIATE", driver.SQLite, txBegin},
		{"START TRANSACTION READ ONLY", driver.MySQL, txBegin},
		{"COMMIT", driver.MySQL, txCommit},
		{"commit work", driver.Postgres, txCommit},
		{"END", driver.Postgres, txCommit},
		{"ROLLBACK", driver.SQLite, txRollback},
		{"ABORT", driver.Postgres, txRollback},
		{"ROLLBACK TO SAVEPOINT a", driver.Postgres, txNone},
		{"ROLLBACK WORK TO a", driver.MySQL, txNone},
		{"COMMIT PREPARED 'x'", driver.Postgres, txNone},
		{"ROLLBACK PREPARED 'x'", driver.Postgres, txNone},
		{"SAVEPOINT a", driver.Postgres, txNone},
		{"START SLAVE", driver.MySQL, txNone},
		{"SELECT 1", driver.Postgres, txNone},
		{"BEGIN; SELECT 1", driver.Postgres, txNone}, // multiple statements: policy rejects it
		{"", driver.Postgres, txNone},
	}
	for _, tt := range tests {
		if got := txControl(tt.sql, tt.d); got != tt.want {
			t.Errorf("txControl(%q) = %d, want %d", tt.sql, got, tt.want)
		}
	}
}

func TestCleanLabel(t *testing.T) {
	if got := cleanLabel("  fix\norder\t 42  "); got != "fix order 42" {
		t.Fatalf("got %q", got)
	}
	if got := cleanLabel(strings.Repeat("é", MaxTxLabel+5)); len([]rune(got)) != MaxTxLabel {
		t.Fatalf("label not capped: %d runes", len([]rune(got)))
	}
}

// count reads t's row count on a fresh pooled session, outside any transaction.
func count(t *testing.T, e *env, p conn.Profile) any {
	t.Helper()
	_, c := e.mustRun(t, p, "SELECT COUNT(*) FROM t")
	return c.rows[0][0]
}

func inTx(id string) func(*Request) { return func(r *Request) { r.TxID = id } }

func TestTxRollbackDiscardsAndCommitKeeps(t *testing.T) {
	for _, how := range []TxEnd{TxRollback, TxCommit} {
		t.Run(string(how), func(t *testing.T) {
			e := newEnv(t)
			p := e.sqlite(t, nil)
			seed(t, e, p, 1)

			tx, err := e.runner.Begin(t.Context(), p.ID, "fix order 42")
			if err != nil {
				t.Fatal(err)
			}
			sum, _ := e.mustRun(t, p, "INSERT INTO t (id, name) VALUES (2, 'b')", inTx(tx.ID))
			if sum.TxID != tx.ID || sum.TxLabel != "fix order 42" || sum.Status != audit.StatusOK {
				t.Fatalf("summary = %+v", sum)
			}
			// Inside the transaction the row is there.
			_, c := e.mustRun(t, p, "SELECT COUNT(*) FROM t", inTx(tx.ID))
			if c.rows[0][0] != int64(2) {
				t.Fatalf("count in tx = %v", c.rows[0][0])
			}
			if got := e.runner.Transactions(); len(got) != 1 || got[0].Statements != 2 || got[0].Writes != 1 {
				t.Fatalf("open = %+v", got)
			}

			end := e.runner.Rollback
			want := int64(1)
			if how == TxCommit {
				end, want = e.runner.Commit, 2
			}
			sum, err = end(t.Context(), tx.ID)
			if err != nil || sum.TxEnd != how || sum.Status != audit.StatusOK {
				t.Fatalf("end = %+v, %v", sum, err)
			}
			if got := count(t, e, p); got != want {
				t.Fatalf("count after %s = %v, want %d", how, got, want)
			}
			if len(e.runner.Transactions()) != 0 {
				t.Fatal("transaction still listed")
			}
			if _, _, err := e.run(t, p, "SELECT 1", inTx(tx.ID)); !errors.Is(err, ErrTxNotFound) {
				t.Fatalf("run after end: %v", err)
			}

			// History tags every statement of the transaction.
			hs, err := e.history.ListTx(t.Context(), tx.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(hs))
			for _, h := range hs {
				if h.TxLabel != "fix order 42" {
					t.Errorf("label = %q", h.TxLabel)
				}
				got = append(got, strings.Fields(h.SQL)[0])
			}
			wantSQL := []string{"BEGIN", "INSERT", "SELECT", strings.ToUpper(string(how))}
			if strings.Join(got, " ") != strings.Join(wantSQL, " ") {
				t.Fatalf("history = %v, want %v", got, wantSQL)
			}

			// The audit log has the write and the outcome, both tagged, and
			// the chain still verifies.
			entries, err := e.audit.List(t.Context(), audit.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			last := entries[len(entries)-2:]
			if last[0].TxID != tx.ID || !strings.HasPrefix(last[0].SQL, "INSERT") ||
				last[1].TxID != tx.ID || last[1].SQL != strings.ToUpper(string(how)) || last[1].Kind != string(KindSession) {
				t.Fatalf("audit tail = %+v", last)
			}
			if v, err := e.audit.Verify(t.Context()); err != nil || !v.OK {
				t.Fatalf("verify = %+v, %v", v, err)
			}
		})
	}
}

func TestTxTypedControl(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 1)

	// Outside a transaction, BEGIN would strand a pooled session.
	sum, _, err := e.run(t, p, "BEGIN")
	if !errors.Is(err, ErrTxControl) || sum.Status != audit.StatusBlocked {
		t.Fatalf("BEGIN outside: %+v, %v", sum, err)
	}
	if _, _, err := e.run(t, p, "COMMIT"); !errors.Is(err, ErrTxControl) {
		t.Fatalf("COMMIT outside: %v", err)
	}

	tx, err := e.runner.Begin(t.Context(), p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.Begin(t.Context(), p.ID, ""); !errors.Is(err, ErrTxOpen) {
		t.Fatalf("second Begin: %v", err)
	}
	if _, _, err := e.run(t, p, "BEGIN", inTx(tx.ID)); !errors.Is(err, ErrTxOpen) {
		t.Fatalf("BEGIN inside: %v", err)
	}
	other := e.sqlite(t, func(p *conn.Profile) { p.Name += " other" })
	if _, _, err := e.run(t, other, "SELECT 1", inTx(tx.ID)); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("tx on another connection: %v", err)
	}
	// Savepoints are ordinary statements inside the transaction.
	e.mustRun(t, p, "SAVEPOINT a", inTx(tx.ID))
	e.mustRun(t, p, "DELETE FROM t", inTx(tx.ID))
	e.mustRun(t, p, "ROLLBACK TO a", inTx(tx.ID))

	e.mustRun(t, p, "INSERT INTO t (id, name) VALUES (2, 'b')", inTx(tx.ID))
	sum, _ = e.mustRun(t, p, "commit;", inTx(tx.ID))
	if sum.TxEnd != TxCommit || sum.Kind != KindSession {
		t.Fatalf("typed COMMIT = %+v", sum)
	}
	if got := count(t, e, p); got != int64(2) {
		t.Fatalf("count = %v", got)
	}
}

func TestTxReadOnlyConnectionStillBlocksWrites(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 1)
	ro := e.profile(t, conn.Profile{Name: "ro", Dialect: driver.SQLite, Database: p.Database, ReadOnly: true})

	tx, err := e.runner.Begin(t.Context(), ro.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.run(t, ro, "DELETE FROM t", inTx(tx.ID)); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("write in read-only tx: %v", err)
	}
	if _, err := e.runner.Rollback(t.Context(), tx.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRollsBackOpenTransactions(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 1)
	tx, err := e.runner.Begin(t.Context(), p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	e.mustRun(t, p, "DELETE FROM t", inTx(tx.ID))
	if err := e.runner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e, p); got != int64(1) {
		t.Fatalf("count after close = %v", got)
	}
	if err := e.runner.RollbackConn(t.Context(), p.ID); err != nil {
		t.Fatalf("RollbackConn with nothing open: %v", err)
	}
}
