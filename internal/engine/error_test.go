package engine

import (
	"errors"
	"fmt"
	"testing"
	"database/sql/driver"
	"strings"

	"stuntgres/internal/sqlstate"
)

func TestAnnotatePreservesWrappedFields(t *testing.T) {
	original := errors.New("sentinel")
	sqlErr := &sqlstate.Error{Code: "22P02", Message: "invalid integer", Schema: "kept", Table: "kept_table", Column: "kept_column"}
	wrapped := fmt.Errorf("binding: %w", sqlErr)
	outer := fmt.Errorf("outer: %w", original)
	combined := fmt.Errorf("%w: %w", outer, wrapped)
	got := annotate(combined, "destination", "destination_column")
	var found *sqlstate.Error
	if !errors.Is(got, original) || !errors.As(got, &found) || found != sqlErr ||
		found.Schema != "kept" || found.Table != "kept_table" || found.Column != "kept_column" {
		t.Fatalf("annotation changed wrapped errors or identities: %#v (%v)", found, got)
	}
}

func TestAnnotateAddsOnlyKnownIdentity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		err           *sqlstate.Error
		table, column string
		wantSchema    string
		wantTable     string
		wantColumn    string
	}{
		{"no object", &sqlstate.Error{Code: "22P02"}, "", "", "", "", ""},
		{"destination", &sqlstate.Error{Code: "22P02"}, "public_table", "column", "public", "public_table", "column"},
		{"column only", &sqlstate.Error{Column: "original"}, "table", "other", "public", "table", "original"},
		{"unrelated schema", &sqlstate.Error{Schema: "other"}, "table", "column", "other", "table", "column"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			annotate(tc.err, tc.table, tc.column)
			if tc.err.Schema != tc.wantSchema || tc.err.Table != tc.wantTable || tc.err.Column != tc.wantColumn {
				t.Fatalf("fields = %#v", tc.err)
			}
		})
	}
}

func TestSQLDiagnosticSeverityAndRowDetail(t *testing.T) {
	err := sqlError("22003", "out of range", "", "")
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" || got.Schema != "" {
		t.Fatalf("SQL error fields = %#v", got)
	}
	for _, tc := range []struct{ row []driver.Value; want string }{
		{[]driver.Value{nil, int64(-9223372036854775808), true, false, "a,b\nc"}, "Failing row contains (null, -9223372036854775808, t, f, a,b\nc)."},
		{[]driver.Value{strings.Repeat("x", 64)}, "Failing row contains (" + strings.Repeat("x", 64) + ")."},
		{[]driver.Value{strings.Repeat("x", 65)}, "Failing row contains (" + strings.Repeat("x", 64) + "...)."},
		{[]driver.Value{strings.Repeat("x", 63) + "é"}, "Failing row contains (" + strings.Repeat("x", 63) + "...)."},
	} {
		if detail := failingRowDetail(tc.row); detail != tc.want {
			t.Errorf("detail = %q, want %q", detail, tc.want)
		}
	}
}
