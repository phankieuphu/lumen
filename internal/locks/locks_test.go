package locks

import (
	"strings"
	"testing"
)

func TestPostgresMeanings(t *testing.T) {
	// Every table lock mode in the PostgreSQL manual has its own meaning.
	modes := []string{
		"AccessShareLock", "RowShareLock", "RowExclusiveLock", "ShareUpdateExclusiveLock",
		"ShareLock", "ShareRowExclusiveLock", "ExclusiveLock", "AccessExclusiveLock",
	}
	seen := map[string]bool{}
	for _, m := range modes {
		got := pgTableMeaning(m)
		if got == "A table lock." || seen[got] {
			t.Errorf("%s: meaning %q is missing or repeated", m, got)
		}
		seen[got] = true
	}
	if !strings.Contains(pgTableMeaning("AccessExclusiveLock"), "reads included") {
		t.Error("ACCESS EXCLUSIVE must say it blocks reads")
	}
}

func TestMySQLRowMeanings(t *testing.T) {
	tests := []struct{ mode, want string }{
		{"X,REC_NOT_GAP", "This row only, for writing"},
		{"S,REC_NOT_GAP", "This row only, for reading"},
		{"X,GAP", "The gap before this row"},
		{"X", "This row and the gap before it, for writing"},
		{"X,GAP,INSERT_INTENTION", "Waiting to insert"},
	}
	for _, tt := range tests {
		if got := myRowMeaning(tt.mode); !strings.HasPrefix(got, tt.want) {
			t.Errorf("%s: %q, want prefix %q", tt.mode, got, tt.want)
		}
	}
}

func TestRowKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"1", "key 1"},
		{"supremum pseudo-record", "after the last row"},
	}
	for _, tt := range tests {
		if got := rowKey(tt.in); got != tt.want {
			t.Errorf("rowKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTruthy(t *testing.T) {
	for _, v := range []any{true, int64(1), "t", "true", "GRANTED", []byte("1")} {
		if !truthy(v) {
			t.Errorf("truthy(%#v) = false", v)
		}
	}
	for _, v := range []any{false, int64(0), "f", "WAITING", nil} {
		if truthy(v) {
			t.Errorf("truthy(%#v) = true", v)
		}
	}
}
