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

// Only the generated foreign metadata repair shape reaches normal binding.
// Bound eligibility (including rejection of scalar 7) is exercised by the
// public default-persistence definition, not guessed by this raw AST gate.
func TestRestoreAdmitsForeignDefaultRepair(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		fail bool
	}{
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT 's.later'::regclass"},
		{sql: `ALTER FOREIGN TABLE "odd""schema"."odd""table" ALTER COLUMN "odd""column" SET DEFAULT 's.later'::regclass`},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT 7"},
		{sql: "ALTER FOREIGN TABLE t ALTER COLUMN c SET DEFAULT 's.later'::regclass", fail: true},
		{sql: "ALTER FOREIGN TABLE IF EXISTS s.t ALTER COLUMN c SET DEFAULT 's.later'::regclass", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c DROP DEFAULT", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT 7, ALTER COLUMN d SET DEFAULT 8", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET NOT NULL", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t OPTIONS(tag 'not-a-default')", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT $1", fail: true},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			statements, err := parser.Parse(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			admission, err := restoreAdmitForeignDDL(new(restoreRun), nil, statements[0])
			if tc.fail {
				if err == nil {
					t.Fatal("unsupported foreign replay shape admitted", admission)
				}
			} else if err != nil || admission != control.Admitted {
				t.Fatal("generated foreign default repair rejected", admission, err)
			}
		})
	}
}
