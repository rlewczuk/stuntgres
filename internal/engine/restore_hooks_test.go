package engine

import (
	"testing"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/parser"
)

// A field step in an INSERT target is not a subscript; it neither consumes
// the statement nor stops the remaining targets' subscript validation.
func TestRestoreAdmitsInsertTargetIndirection(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want control.Admission
		fail bool
	}{
		{sql: "INSERT INTO t (c.f, a[1]) VALUES (1, 2)", want: control.Admitted},
		{sql: "INSERT INTO t (c.f, a[$1]) VALUES (1, 2)", fail: true},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.sql, err)
		}
		answer, err := restoreAdmitChecked(admitRestoreInsert)(new(restoreRun), nil, statements[0])
		if tc.fail {
			if err == nil {
				t.Fatalf("%q admitted as %v, want an error", tc.sql, answer)
			}
			continue
		}
		if err != nil || answer != tc.want {
			t.Fatalf("%q = %v, %v; want %v", tc.sql, answer, err, tc.want)
		}
	}
}
