package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryMergedStoredExclusions(t *testing.T) {
	for _, tc := range []struct{ name, expr, token string }{
		{"exists", "EXISTS(SELECT missing FROM absent)", "EXISTS"},
		{"any", "1 = ANY(SELECT missing FROM absent)", "="},
		{"all-width", "1 = ALL(SELECT 1,2)", "="},
		{"row", "ROW(1,2) = (SELECT missing FROM absent)", "="},
		{"dead", "CASE WHEN FALSE THEN EXISTS(SELECT missing FROM absent) ELSE FALSE END", "EXISTS"},
	} {
		for _, consumer := range []string{"generated", "index"} {
			t.Run(consumer+"/"+tc.name, func(t *testing.T) {
				subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := subqueryRun(t, p, b, c)
					q := "/*é*/ CREATE TABLE excluded_generated(a INT,b BOOL GENERATED ALWAYS AS(" + tc.expr + ") STORED)"
					message := "cannot use subquery in column generation expression"
					if consumer == "index" {
						run(fixture.Exec("CREATE TABLE excluded_base(a INT)", 0))
						run(fixture.Exec("INSERT INTO excluded_base VALUES(1),(2)", 2))
						q = "/*é*/ CREATE INDEX excluded_index ON excluded_base ((" + tc.expr + "))"
						message = "cannot use subquery in index expression"
					}
					want := fixture.ControlsReferenceDiagnostic("0A000", message)
					want.Position = fixture.GeneratedStoredPosition(q, tc.token)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, want)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
					}
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='excluded_generated' OR relname='excluded_index'", []string{"relname"}, []string{"NAME"}, nil))
					if consumer == "index" {
						subqueryBoth(run, fixture.Select("SELECT a FROM excluded_base ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						run(fixture.Exec("CREATE INDEX excluded_index ON excluded_base(a)", 0))
					} else {
						run(fixture.Exec("CREATE TABLE excluded_generated(a INT)", 0))
					}
				})
			})
		}
	}
}
