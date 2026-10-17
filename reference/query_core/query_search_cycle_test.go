package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchCycleInterop(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		cycleSeed(run)
		for _, mode := range searchModes {
			t.Run(mode+" closing row and star", func(t *testing.T) {
				q := "WITH RECURSIVE r(n) AS (SELECT 'a'::text UNION ALL SELECT e.dst FROM r JOIN cycle_edges e ON e.src=r.n)" + searchClause(mode, "n") + "CYCLE n SET cyc USING path SELECT * FROM r ORDER BY seq"
				want := [][]any{{"a", "{(a)}", false, "{(a)}"}, {"b", "{(a),(b)}", false, "{(a),(b)}"}, {"a", "{(a),(b),(a)}", true, "{(a),(b),(a)}"}, {"c", "{(a),(c)}", false, "{(a),(c)}"}}
				if mode == "BREADTH" {
					want = [][]any{{"a", "(0,a)", false, "{(a)}"}, {"b", "(1,b)", false, "{(a),(b)}"}, {"c", "(1,c)", false, "{(a),(c)}"}, {"a", "(2,a)", true, "{(a),(b),(a)}"}}
				}
				recursiveCTERoutes(t, p, b, c, fixture.Select(q, []string{"n", "seq", "cyc", "path"}, []string{"TEXT", searchType(mode), "BOOL", "_RECORD"}, want))
			})
			t.Run(mode+" independent key lists", func(t *testing.T) {
				q := "WITH RECURSIVE r(n,k) AS (SELECT 1,0 UNION ALL SELECT n,k+1 FROM r WHERE k<3)" + searchClause(mode, "k") + "CYCLE n SET cyc USING path SELECT * FROM r ORDER BY seq"
				seq1, seq2 := "{(0)}", "{(0),(1)}"
				if mode == "BREADTH" {
					seq1, seq2 = "(0,0)", "(1,1)"
				}
				subqueryBoth(run, fixture.Select(q, []string{"n", "k", "seq", "cyc", "path"}, []string{"INT4", "INT4", searchType(mode), "BOOL", "_RECORD"}, [][]any{{int64(1), int64(0), seq1, false, "{(1)}"}, {int64(1), int64(1), seq2, true, "{(1),(1)}"}}))
			})
			t.Run(mode+" contained stars and generated identity", func(t *testing.T) {
				for _, union := range []string{"UNION ALL", "UNION"} {
					base := "WITH RECURSIVE r(n) AS (SELECT 1 " + union + " SELECT r.* FROM r)" + searchClause(mode, "n") + "CYCLE n SET cyc USING path "
					seq1, seq2 := "{(1)}", "{(1),(1)}"
					if mode == "BREADTH" {
						seq1, seq2 = "(0,1)", "(1,1)"
					}
					subqueryBoth(run, cteBag(base+"SELECT * FROM r", []string{"n", "seq", "cyc", "path"}, []string{"INT4", searchType(mode), "BOOL", "_RECORD"}, [][]any{{int64(1), seq1, false, "{(1)}"}, {int64(1), seq2, true, "{(1),(1)}"}}))
				}
				for _, query := range []string{
					"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.* FROM r WHERE FALSE)" + searchClause(mode, "n") + ", s AS (SELECT * FROM r) SELECT * FROM s",
					"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.* FROM r WHERE FALSE)" + searchClause(mode, "n") + ", s AS (SELECT * FROM (SELECT * FROM r) x) SELECT * FROM s",
					"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT x FROM r AS t(x) WHERE FALSE)" + searchClause(mode, "n") + ", s AS (SELECT * FROM r) SELECT * FROM s",
				} {
					subqueryBoth(run, fixture.Select(query, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1)))
				}
			})
			for _, tc := range []struct{ suffix, message string }{
				{"CYCLE n SET seq USING path SELECT * FROM r", "search sequence column name and cycle mark column name are the same"},
				{"CYCLE n SET cyc USING seq SELECT * FROM r", "search sequence column name and cycle path column name are the same"},
			} {
				t.Run(mode+" "+tc.message, func(t *testing.T) {
					q := "/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1 FROM r)" + searchClause(mode, "n") + tc.suffix
					want := cycleDiagnostic("42601", tc.message, q, "SEARCH")
					searchAssertDiagnostic(t, p, b, c, q, &want)
				})
			}
			t.Run(mode+" CYCLE own error precedence", func(t *testing.T) {
				q := "/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1 FROM r)" + searchClause(mode, "n") + "CYCLE missing SET seq USING path SELECT * FROM r"
				want := cycleDiagnostic("42601", `cycle column "missing" not in WITH query column list`, q, "CYCLE")
				searchAssertDiagnostic(t, p, b, c, q, &want)
			})
		}
	})
}
