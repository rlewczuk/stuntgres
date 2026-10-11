package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeBinaryParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(b BYTEA)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES('\\x00ff')")
			stmt, err := db.Prepare("SELECT b LIKE $1::bytea ESCAPE $2::bytea FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			pattern := []byte{0, '%'}
			rows, err := stmt.Query(pattern, []byte{'!'})
			pattern[0] = 1
			fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{true}}, false)
			rows, err = stmt.Query([]byte{'_'}, []byte{})
			fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{false}}, false)
			rows, err = stmt.Query([]byte{'_', '_'}, nil)
			fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{nil}}, false)
			rows, err = stmt.Query([]byte{'%'}, []byte{0xc3, 0xa9})
			if rows != nil {
				rows.Close()
				t.Fatal("invalid binary escape exposed rows")
			}
			fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
		})
	}
}

func TestReferenceLikeDiagnosticsAndHeldEscapes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(i INTEGER,v TEXT,b BYTEA)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1,'a%b','\\x61')")
			hint := "No operator matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"SELECT i LIKE 'x' FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer ~~ unknown", Hint: hint, Position: "10"}},
				{"SELECT i NOT ILIKE 'x' FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer !~~* unknown", Hint: hint, Position: "10"}},
				{"SELECT /*é*/ i LIKE 'x' FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer ~~ unknown", Hint: hint, Position: "16"}},
				{"SELECT b ILIKE b FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: bytea ~~* bytea", Hint: hint, Position: "10"}},
				{"SELECT v LIKE 'x' ESCAPE 1 FROM t", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.like_escape(unknown, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "10"}},
			} {
				rows, err := db.Query(tc.q)
				if rows != nil {
					rows.Close()
					t.Fatal("failed query exposed cursor")
				}
				fixture.StringCoreControlDiagnostic(t, err, tc.want)
				stmt, err := db.Prepare(tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed prepare exposed handle")
				}
				fixture.StringCoreControlDiagnostic(t, err, tc.want)
			}
			stmt, err := db.Prepare("SELECT v LIKE $1 ESCAPE $2 FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.Query("a!%b", "ab")
			if rows != nil {
				rows.Close()
				t.Fatal("failed execute exposed cursor")
			}
			fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
			for _, tc := range []struct {
				escape any
				want   any
			}{{"!", true}, {nil, nil}, {"!", true}} {
				rows, err := stmt.Query("a!%b", tc.escape)
				fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{tc.want}}, false)
			}
		})
	}
}
