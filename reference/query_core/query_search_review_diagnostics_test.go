package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Review regressions isolate body analysis, SEARCH analysis and rewrite errors.
// Expected diagnostics were confirmed against pinned PostgreSQL 18.6;
// the shared assertion checks every logical field, Unicode position, publication
// boundary and direct/prepared failure phase, not merely SQLSTATE parity.
func TestReferenceQuerySearchReviewDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := cycleConn(t, p, b)
			const leftForm = "with a SEARCH or CYCLE clause, the left side of the UNION must be a SELECT"
			const rightForm = "with a SEARCH or CYCLE clause, the right side of the UNION must be a SELECT"
			const buriedForm = `with a SEARCH or CYCLE clause, the recursive reference to WITH query "r" must be at the top level of its right-hand SELECT`
			const missingKey = `search column "missing" not in WITH query column list`
			const aliasWidth = `WITH query "r" has 1 columns available but 2 columns specified`
			for _, mode := range searchModes {
				clause := searchClause(mode, "n")
				missing := searchClause(mode, "missing")
				duplicate := searchClause(mode, "n,n")
				conflict := " SEARCH " + mode + " FIRST BY n SET n "
				const left = "WITH RECURSIVE r(n) AS ((SELECT 1 UNION ALL SELECT 2) UNION ALL SELECT n FROM r)"
				const right = "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT 2))"
				const buried = "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM (SELECT n FROM r) s)"
				// No reference to n in this body: SET n must report the name
				// conflict, rather than an earlier ambiguous body reference.
				const buriedConstant = "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1 FROM (SELECT 1 FROM r) s)"
				for _, tc := range []struct{ name, head, suffix, code, message, marker string }{
					// R1: the review's exact nested-left/body-error example,
					// then the corresponding RHS and nested-self boundaries.
					{"R1 left body missing", "WITH RECURSIVE r(n) AS ((SELECT missing UNION ALL SELECT 2) UNION ALL SELECT n FROM r)", clause, "42703", `column "missing" does not exist`, "missing UNION"},
					{"R1 left recursive body missing", "WITH RECURSIVE r(n) AS ((SELECT 1 UNION ALL SELECT 2) UNION ALL SELECT missing FROM r)", clause, "42703", `column "missing" does not exist`, "missing FROM"},
					{"R1 right body missing", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT missing FROM r UNION ALL SELECT 2))", clause, "42703", `column "missing" does not exist`, "missing FROM"},
					{"R1 buried anchor missing", "WITH RECURSIVE r(n) AS (SELECT missing UNION ALL SELECT n FROM (SELECT n FROM r) s)", clause, "42703", `column "missing" does not exist`, "missing UNION"},
					{"R1 buried recursive body missing", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT missing FROM (SELECT n FROM r) s)", missing, "42703", `column "missing" does not exist`, "missing FROM"},
					{"R1 left width before form", "WITH RECURSIVE r(n) AS ((SELECT 1 UNION ALL SELECT 2,3) UNION ALL SELECT n FROM r)", missing, "42601", "each UNION query must have the same number of columns", "2,3"},
					{"R1 right width before form", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT 2,3))", missing, "42601", "each UNION query must have the same number of columns", "2,3"},
					{"R1 left type before form", "WITH RECURSIVE r(n) AS ((SELECT 1 UNION ALL SELECT 'x'::text) UNION ALL SELECT n FROM r)", missing, "42804", "UNION types integer and text cannot be matched", "'x'"},
					{"R1 right type before form", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT 'x'::text))", missing, "42804", "UNION types integer and text cannot be matched", "'x'"},
					// Arm expandability is analysis-time and precedes SEARCH
					// names; top-level-self is rewrite-time and follows them.
					{"R1 left standalone form", left, clause, "0A000", leftForm, ""},
					{"R1 left form before missing key", left, missing, "0A000", leftForm, ""},
					{"R1 left form before duplicate key", left, duplicate, "0A000", leftForm, ""},
					{"R1 right standalone form", right, clause, "42601", rightForm, ""},
					{"R1 right form before missing key", right, missing, "42601", rightForm, ""},
					{"R1 right form before duplicate key", right, duplicate, "42601", rightForm, ""},
					{"R1 buried standalone form", buried, clause, "0A000", buriedForm, ""},
					{"R1 buried missing key before rewrite", buried, missing, "42601", missingKey, "SEARCH"},
					{"R1 buried duplicate key before rewrite", buried, duplicate, "42701", `search column "n" specified more than once`, "SEARCH"},
					{"R1 buried SET conflict before rewrite", buriedConstant, conflict, "42601", `search sequence column name "n" already used in WITH query column list`, "SEARCH"},
					// R2: WITH RECURSIVE does not imply actual self-recursion.
					// Both used and unused items must retain the alias error.
					{"R2 plain excess aliases", "WITH r(n,x) AS (SELECT 1)", clause, "42P10", aliasWidth, "r(n,x)"},
					{"R2 recursive modifier excess aliases", "WITH RECURSIVE r(n,x) AS (SELECT 1)", clause, "42P10", aliasWidth, "r(n,x)"},
					{"R2 plain aliases before missing key", "WITH r(n,x) AS (SELECT 1)", missing, "42P10", aliasWidth, "r(n,x)"},
					{"R2 recursive aliases before missing key", "WITH RECURSIVE r(n,x) AS (SELECT 1)", missing, "42P10", aliasWidth, "r(n,x)"},
					{"R2 plain valid width", "WITH r(n) AS (SELECT 1)", clause, "42601", "WITH query is not recursive", "r(n)"},
					{"R2 recursive modifier valid width", "WITH RECURSIVE r(n) AS (SELECT 1)", clause, "42601", "WITH query is not recursive", "r(n)"},
				} {
					for _, prefix := range []string{"", "/*é*/ "} {
						for _, consumer := range []string{"SELECT * FROM r", "SELECT 7"} {
							t.Run(mode+" "+tc.name+prefix+" "+consumer, func(t *testing.T) {
								q := prefix + tc.head + tc.suffix + consumer
								want := cycleDiagnostic(tc.code, tc.message, q, tc.marker)
								searchAssertDiagnostic(t, p, b, c, q, &want)
							})
						}
					}
				}
			}

			// Positive controls protect ordinary recursion's wider legal forms
			// and CYCLE's closing row/stop semantics on all existing routes.
			for _, tc := range []struct {
				name, q string
				rows    [][]any
			}{
				{"ordinary finite", recursiveCTEFinite + " ORDER BY n", recursiveCTEInts(1, 2, 3)},
				{"ordinary nested left", "WITH RECURSIVE r(n) AS ((SELECT 1 UNION ALL SELECT 2) UNION ALL SELECT n+2 FROM r WHERE n<3) SELECT n FROM r ORDER BY n", recursiveCTEInts(1, 2, 3, 4)},
				{"ordinary nested right", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n+1 FROM r WHERE n<2 UNION ALL SELECT 3)) SELECT n FROM r LIMIT 3", recursiveCTEInts(1, 2, 3)},
				{"ordinary buried self", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM (SELECT n FROM r) s WHERE n<3) SELECT n FROM r ORDER BY n", recursiveCTEInts(1, 2, 3)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					recursiveCTERoutes(t, p, b, c, fixture.Select(tc.q, []string{"n"}, []string{"INT4"}, tc.rows))
				})
			}
			t.Run("CYCLE closing row", func(t *testing.T) {
				q := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT n,cyc,path FROM r ORDER BY cyc"
				recursiveCTERoutes(t, p, b, c, fixture.Select(q, []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}))
			})
		})
	}
}
