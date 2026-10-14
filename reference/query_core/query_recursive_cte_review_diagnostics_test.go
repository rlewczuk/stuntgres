package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceQueryRecursiveCTEReviewDiagnostics(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE recursive_review_one(n INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_review_one VALUES(99)", 1))
		const subquery = "recursive reference to query \"r\" must not appear within a subquery"
		// Expected records are release-source-derived, not native observations.
		// The unique table marker identifies the independently responsible
		// reference; UTF8 prefixes retain character, not byte, positions.
		for _, tc := range []struct{ name, body, message, marker string }{
			{"body_with_self", "WITH a AS (SELECT n FROM r AS forbidden) SELECT 1 UNION ALL SELECT n FROM a", subquery, "r AS forbidden"},
			{"body_with_before_order", "WITH a AS (SELECT n FROM r AS forbidden) SELECT 1 UNION ALL SELECT n FROM a ORDER BY n", subquery, "r AS forbidden"},
			{"body_with_before_offset_limit", "WITH a AS (SELECT n FROM r AS forbidden) SELECT 1 UNION ALL SELECT n FROM a OFFSET 1 LIMIT 2", subquery, "r AS forbidden"},
			{"body_with_before_limit", "WITH a AS (SELECT n FROM r AS forbidden) SELECT 1 UNION ALL SELECT n FROM a LIMIT 2", subquery, "r AS forbidden"},
			{"body_with_before_lock", "WITH a AS (SELECT n FROM r AS forbidden) SELECT 1 UNION ALL SELECT n FROM a FOR UPDATE", subquery, "r AS forbidden"},
			{"body_recursive_with_forward_self", "WITH RECURSIVE a AS (SELECT n FROM b),b AS (SELECT n FROM r AS forbidden) SELECT 1 UNION ALL SELECT n FROM a", subquery, "r AS forbidden"},
			{"anchor_scalar_overrides", "SELECT (SELECT n FROM r AS forbidden) UNION ALL SELECT 1", subquery, "r AS forbidden"},
			{"anchor_in_query_before_test", "SELECT CASE WHEN (SELECT n FROM r AS test_ref) IN (SELECT n FROM r AS forbidden) THEN 1 ELSE 0 END UNION ALL SELECT 1", subquery, "r AS forbidden"},
			{"rhs_in_query_before_test", "SELECT 1 UNION ALL SELECT CASE WHEN (SELECT n FROM r AS test_ref) IN (SELECT n FROM r AS forbidden) THEN 1 ELSE 0 END", subquery, "r AS forbidden"},
			{"nullable_left_scalar_overrides", "SELECT 1 UNION ALL SELECT x.n FROM recursive_review_one o LEFT JOIN (SELECT (SELECT n FROM r AS forbidden) AS n) x ON TRUE", subquery, "r AS forbidden"},
			{"nullable_right_scalar_overrides", "SELECT 1 UNION ALL SELECT x.n FROM (SELECT (SELECT n FROM r AS forbidden) AS n) x RIGHT JOIN recursive_review_one o ON TRUE", subquery, "r AS forbidden"},
			{"nullable_full_exists_overrides", "SELECT 1 UNION ALL SELECT 1 FROM recursive_review_one o FULL JOIN (SELECT 1 AS n WHERE EXISTS(SELECT n FROM r AS forbidden)) x ON TRUE", subquery, "r AS forbidden"},
			{"intersect_all_scalar_overrides", "SELECT 1 UNION ALL (SELECT (SELECT n FROM r AS forbidden) INTERSECT ALL SELECT 1)", subquery, "r AS forbidden"},
			{"except_all_left_scalar_overrides", "SELECT 1 UNION ALL (SELECT (SELECT n FROM r AS forbidden) EXCEPT ALL SELECT 1)", subquery, "r AS forbidden"},
			{"except_right_scalar_overrides", "SELECT 1 UNION ALL (SELECT 1 EXCEPT SELECT (SELECT n FROM r AS forbidden))", subquery, "r AS forbidden"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := subqueryRun(t, p, b, c)
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "WITH RECURSIVE r(n) AS (" + tc.body + ") SELECT n FROM r"
					index := strings.Index(q, tc.marker)
					if index < 0 {
						t.Fatal("missing responsible reference marker", tc.marker)
					}
					want := fixture.ControlsReferenceDiagnostic("42P19", tc.message)
					want.Position = fixture.Position(utf8.RuneCountInString(q[:index]) + 1)
					subqueryAnalysisFailure(run, q, want)
					run(cteBag("SELECT n FROM recursive_review_one", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
				}
			})
		}
		for _, tc := range []struct {
			name, body string
			rows       [][]any
		}{
			{"harmless_body_with", "WITH a AS (SELECT 1 AS step) SELECT 1 UNION ALL SELECT r.n+a.step FROM r CROSS JOIN a WHERE r.n<3", recursiveCTEInts(1, 2, 3)},
			{"harmless_body_recursive_with", "WITH RECURSIVE a AS (SELECT 1 AS step) SELECT 1 UNION ALL SELECT r.n+a.step FROM r CROSS JOIN a WHERE r.n<3", recursiveCTEInts(1, 2, 3)},
			{"body_with_local_shadow", "WITH r(n) AS (SELECT 7) SELECT n FROM r UNION ALL SELECT 8", recursiveCTEInts(7, 8)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "WITH RECURSIVE r(n) AS (" + tc.body + ") SELECT n FROM r"
				s := cteBag(q, []string{"n"}, []string{"INT4"}, tc.rows)
				s.CheckMetadata = recursiveCTEMetadata(b, s.Columns, s.Types)
				recursiveCTERoutes(t, p, b, c, s)
			})
		}
	})
}
