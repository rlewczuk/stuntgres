package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateFromQueryDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, code, message, token, detail, hint string }{
		{"UPDATE t SET v=1 FROM t RETURNING *", "42712", "table name \"t\" specified more than once", "", "", ""},
		{"/*é*/ UPDATE t x SET v=t.v FROM s RETURNING *", "42P01", "invalid reference to FROM-clause entry for table \"t\"", "t.v", "", "Perhaps you meant to reference the table alias \"x\"."},
		{"UPDATE t x SET v=s.n FROM s JOIN r ON x.id=s.k RETURNING *", "42P01", "invalid reference to FROM-clause entry for table \"x\"", "x.id", "There is an entry for table \"x\", but it cannot be referenced from this part of the query.", ""},
	} {
		t.Run(tc.q, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				run(fixture.Exec("CREATE TABLE r(k INT)", 0))
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Detail = tc.detail
				want.Hint = tc.hint
				if tc.token != "" {
					want.Position = updateFromPosition(tc.q, tc.token)
				}
				s := fixture.ErrorDiagnostic(tc.q, want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				run(updateFromTargets(updateFromOriginal()))
			})
		})
	}
}

func TestReferenceUpdateFromSourceTypeDiagnostics(t *testing.T) {
	for _, tc := range []struct{ left, right, code, message string }{
		{"id INT,id INT", "id INT", "42702", "common column name \"id\" appears more than once in left table"},
		{"id INT", "id BOOL", "42883", "operator does not exist: integer = boolean"},
	} {
		// Duplicate source columns cannot be created in a base table; positional
		// aliases intentionally expose duplicate names on a valid two-column source.
		t.Run(tc.code, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(10)", 1))
				source := "s JOIN r USING(id)"
				if tc.code == "42702" {
					run(fixture.Exec("CREATE TABLE s(a INT,b INT)", 0))
					source = "s AS s(id,id) JOIN r USING(id)"
				} else {
					run(fixture.Exec("CREATE TABLE s(id INT)", 0))
				}
				run(fixture.Exec("CREATE TABLE r("+tc.right+")", 0))
				q := "UPDATE t SET v=1 FROM " + source
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.code == "42883" {
					want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				}
				updateFromDiagnostic(t, p.Ctx, c, q, want, false)
				run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			})
		})
	}
}
