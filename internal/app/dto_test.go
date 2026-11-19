package app

import (
	"reflect"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/query"
	"github.com/phankieuphu/lumen/internal/sqlparse"
)

func TestStatementDTOsUseUTF16Offsets(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []StatementDTO
	}{
		{"ascii", "SELECT 1; SELECT 2", []StatementDTO{{0, 8}, {10, 18}}},
		// é is 2 bytes and 1 UTF-16 unit; 😀 is 4 bytes and 2 units.
		{"multibyte", "SELECT 'é';\nSELECT '😀'; DELETE FROM t", []StatementDTO{{0, 10}, {12, 23}, {25, 38}}},
		{"none", " -- only a comment", []StatementDTO{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statementDTOs(tt.sql, query.Spans(tt.sql, driver.Postgres))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSyntaxIssueDTOsUseUTF16Offsets(t *testing.T) {
	// 😀 is 4 bytes and 2 UTF-16 units; FORM starts at byte 14, unit 12.
	sql := "SELECT '😀' x FORM t"
	got := syntaxIssueDTOs(sql, []sqlparse.Issue{{Start: 14, End: 18, Message: "m"}})
	want := []SyntaxIssueDTO{{From: 12, To: 16, Message: "m"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
