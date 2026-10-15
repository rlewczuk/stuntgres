package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowCompareDiagnostics(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Independently specified full logical records, validated against pinned
		// PostgreSQL 18.0, including all optional-field absences.
		for _, tc := range []struct {
			q, code, message string
			pos              int
			hint             string
		}{
			{"SELECT ROW(1,2) = ROW(1)", "42601", "unequal number of entries in row expressions", 17, ""},
			{"SELECT ROW(1,2) IS DISTINCT FROM ROW(1)", "42601", "unequal number of entries in row expressions", 17, ""},
			{"SELECT ROW(1,2) IS NOT DISTINCT FROM ROW(1)", "42601", "unequal number of entries in row expressions", 17, ""},
			{"SELECT ROW() = ROW()", "0A000", "cannot compare rows of zero length", 14, ""},
			{"SELECT ROW(1,2) = ROW(1,TRUE)", "42883", "operator does not exist: integer = boolean", 17, "No operator matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT ROW(1,2) IS DISTINCT FROM ROW(1,TRUE)", "42883", "operator does not exist: integer = boolean", 17, "No operator matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT ROW(1,2) IS NOT DISTINCT FROM ROW(1,TRUE)", "42883", "operator does not exist: integer = boolean", 17, "No operator matches the given name and argument types. You might need to add explicit type casts."},
			// A decisive first field must not bypass later field analysis.
			{"SELECT ROW(0,2) = ROW(1,TRUE)", "42883", "operator does not exist: integer = boolean", 17, "No operator matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT ROW(0,2) IS DISTINCT FROM ROW(1,TRUE)", "42883", "operator does not exist: integer = boolean", 17, "No operator matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT ROW(1,2) = ANY(SELECT 1,TRUE)", "42883", "operator does not exist: integer = boolean", 17, "No operator matches the given name and argument types. You might need to add explicit type casts."},
		} {
			for _, utf8 := range []bool{false, true} {
				q, pos := tc.q, tc.pos
				if utf8 {
					q = strings.Replace(q, "SELECT ", "SELECT /*é*/ ", 1)
					pos += 6
				}
				t.Run(q, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Position, want.Hint = fixture.Position(pos), tc.hint
					run := subqueryRun(t, p, b, c)
					subqueryAnalysisFailure(run, q, want)
					subqueryBoth(run, fixture.Select("SELECT ROW(1,2) = ROW(1,2) AS answer", []string{"answer"}, []string{"BOOL"}, [][]any{{true}}))
				})
			}
		}
	})
}

func TestReferenceRowCompareConstructors(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"SELECT ROW(t.*) = ROW(1,2) AS answer FROM (SELECT 1 AS a,2 AS b) t",
			"SELECT ROW(t.*) IS NOT DISTINCT FROM ROW(1,2) AS answer FROM (SELECT 1 AS a,2 AS b) t",
			"SELECT ROW(t.*) IN (SELECT 1,2) AS answer FROM (SELECT 1 AS a,2 AS b) t",
			"SELECT ROW(t.*) = ANY(SELECT 1,2) AS answer FROM (SELECT 1 AS a,2 AS b) t",
		} {
			t.Run(q, func(t *testing.T) {
				subqueryBoth(subqueryRun(t, p, b, c), fixture.Select(q, []string{"answer"}, []string{"BOOL"}, [][]any{{true}}))
			})
		}
		for _, q := range []string{
			"SELECT ROW(missing) = ROW(1,2)",
			"SELECT ROW(missing) IS DISTINCT FROM ROW(1,2)",
			"SELECT ROW(1) = ROW(1,missing)",
			"SELECT ROW(1) IS DISTINCT FROM ROW(1,missing)",
		} {
			for _, utf8 := range []bool{false, true} {
				q, pos := q, strings.Index(q, "missing")+1
				if utf8 {
					q = strings.Replace(q, "SELECT ", "SELECT /*é*/ ", 1)
					pos += 6
				}
				t.Run(q, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
					want.Position = fixture.Position(pos)
					subqueryAnalysisFailure(subqueryRun(t, p, b, c), q, want)
				})
			}
		}
		for _, width := range []int{1664, 1665} {
			fields := strings.TrimSuffix(strings.Repeat("1,", width), ",")
			for _, op := range []string{" = ", " IS NOT DISTINCT FROM "} {
				q := "SELECT ROW(" + fields + ")" + op + "ROW(" + fields + ") AS answer"
				name := "maximum" + op
				if width == 1665 {
					name = "over_maximum" + op
				}
				t.Run(name, func(t *testing.T) {
					run := subqueryRun(t, p, b, c)
					if width == 1664 {
						subqueryBoth(run, fixture.Select(q, []string{"answer"}, []string{"BOOL"}, [][]any{{true}}))
						return
					}
					want := fixture.ControlsReferenceDiagnostic("54011", "ROW expressions can have at most 1664 entries")
					want.Position = "8"
					subqueryAnalysisFailure(run, q, want)
				})
			}
		}
	})
}
