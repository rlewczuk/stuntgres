package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLateralScope(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		lateralFixture(run)
		// The same four local rows repeat for EACH prefix, retaining duplicates.
		for _, tc := range []struct {
			expression       string
			inherited, local bool
		}{
			{"t.v", true, false},
			{"u.k", false, true},
			{"t.v+u.k", true, true},
			{"(SELECT t.v+u.k)", true, true},
		} {
			var want [][]any
			for i, v := range []any{int64(2), int64(3), nil} {
				for _, k := range []int64{1, 1, 2, 4} {
					var z any
					switch {
					case !tc.inherited:
						z = k
					case !tc.local:
						z = v
					case v != nil:
						z = v.(int64) + k
					}
					want = append(want, []any{int64(i + 1), k, z})
				}
			}
			q := "SELECT t.id,u.k,s.z FROM lat_t t,(lat_u u CROSS JOIN LATERAL (SELECT " + tc.expression + " AS z) s) ORDER BY t.id,u.k"
			subqueryBoth(run, fixture.Select(q, []string{"id", "k", "z"}, []string{"INT4", "INT4", "INT4"}, want))
		}
		// A nested local SELECT must not export its own same-numbered frame slots.
		subqueryBoth(run, fixture.Select("SELECT t.id,x.n FROM lat_t t CROSS JOIN LATERAL (SELECT (SELECT u.k FROM lat_u u WHERE u.w='c')+t.v AS n) x ORDER BY t.id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(2), int64(5)}, {int64(3), nil}}))
		for _, join := range []string{"RIGHT", "FULL"} {
			subqueryBoth(run, fixture.Select("SELECT t.id,x.n FROM lat_t t "+join+" JOIN LATERAL (SELECT 42 AS n) x ON true ORDER BY t.id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(42)}, {int64(2), int64(42)}, {int64(3), int64(42)}}))
			subqueryBoth(run, fixture.Select("SELECT t.id,x.n FROM lat_t t "+join+" JOIN LATERAL (VALUES(42)) x(n) ON true ORDER BY t.id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(42)}, {int64(2), int64(42)}, {int64(3), int64(42)}}))
			// Ancestor-prefix capture is legal: it does NOT read this JOIN's left.
			subqueryBoth(run, fixture.Select("SELECT t.id,u.k,x.n FROM lat_t t,(lat_u u "+join+" JOIN LATERAL (SELECT t.v AS n) x ON u.k=x.n) ORDER BY t.id,u.k NULLS LAST", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, lateralAncestorRows(join)))
		}
		const unavailable = `There is an entry for table "t", but it cannot be referenced from this part of the query.`
		const mark = "To reference that table, you must mark this subquery with LATERAL."
		const combining = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
		cases := []struct{ q, token, code, message, detail, hint string }{
			{"SELECT * FROM LATERAL (SELECT u.w) s, lat_u u", "u.w", "42P01", `missing FROM-clause entry for table "u"`, "", ""},
			{"SELECT * FROM LATERAL (VALUES(u.w)) s, lat_u u", "u.w", "42P01", `missing FROM-clause entry for table "u"`, "", ""},
			{"SELECT * FROM lat_t t, (SELECT t.v) x", "t.v", "42P01", `invalid reference to FROM-clause entry for table "t"`, unavailable, mark},
			{"SELECT * FROM lat_t t, (VALUES(t.v)) x", "t.v", "42P01", `invalid reference to FROM-clause entry for table "t"`, unavailable, mark},
			{"/* λ雪 */ SELECT * FROM lat_t t, (SELECT t.v) x", "t.v", "42P01", `invalid reference to FROM-clause entry for table "t"`, unavailable, mark},
			{"SELECT * FROM lat_t t RIGHT JOIN LATERAL (SELECT t.v AS m) x ON true", "t.v", "42P10", `invalid reference to FROM-clause entry for table "t"`, combining, ""},
			{"SELECT * FROM lat_t t FULL JOIN LATERAL (SELECT t.v AS m) x ON true", "t.v", "42P10", `invalid reference to FROM-clause entry for table "t"`, combining, ""},
			{"SELECT * FROM lat_t t RIGHT JOIN LATERAL (VALUES(t.v)) x ON true", "t.v", "42P10", `invalid reference to FROM-clause entry for table "t"`, combining, ""},
			{"SELECT * FROM lat_t t FULL JOIN LATERAL (SELECT (SELECT t.v) AS m) x ON true", "t.v", "42P10", `invalid reference to FROM-clause entry for table "t"`, combining, ""},
			{"SELECT * FROM lat_t t, LATERAL (VALUES (t.v)) v(x,y)", "", "42P10", `table "v" has 1 columns available but 2 columns specified`, "", ""},
			{"SELECT * FROM lat_t t, LATERAL (SELECT t.v) v(x,y)", "", "42P10", `table "v" has 1 columns available but 2 columns specified`, "", ""},
			{"SELECT * FROM LATERAL (lat_t t JOIN lat_u u ON true) z", "lat_t", "42601", `syntax error at or near "lat_t"`, "", ""},
			{"SELECT * FROM lat_t t,LATERAL (SELECT t.v) x trailing", "trailing", "42601", `syntax error at or near "trailing"`, "", ""},
			{"SELECT * FROM lat_t t,LATERAL (SELECT v FROM lat_t a,lat_t b) x", "v FROM", "42702", `column reference "v" is ambiguous`, "", ""},
			{"SELECT * FROM lat_t t,(SELECT v) x", "v)", "42703", `column "v" does not exist`, `There is a column named "v" in table "t", but it cannot be referenced from this part of the query.`, "To reference that column, you must mark this subquery with LATERAL."},
		}
		for _, tc := range cases {
			subqueryAnalysisFailure(run, tc.q, lateralDiagnostic(tc.q, tc.token, tc.code, tc.message, tc.detail, tc.hint))
		}
		end := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
		end.Position = "28"
		subqueryAnalysisFailure(run, "SELECT * FROM LATERAL lat_t", end)
		// Failed child binding must restore the ordinary namespace before reuse.
		subqueryBoth(run, lateralChainStep())
		subqueryBoth(run, fixture.Select("SELECT t.id,x.v FROM lat_t t,(SELECT 7 AS v) x ORDER BY t.id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}}))
	})
}

func lateralAncestorRows(join string) [][]any {
	if join == "RIGHT" {
		return [][]any{{int64(1), int64(2), int64(2)}, {int64(2), nil, int64(3)}, {int64(3), nil, nil}}
	}
	return [][]any{
		{int64(1), int64(1), nil}, {int64(1), int64(1), nil}, {int64(1), int64(2), int64(2)}, {int64(1), int64(4), nil},
		{int64(2), int64(1), nil}, {int64(2), int64(1), nil}, {int64(2), int64(2), nil}, {int64(2), int64(4), nil}, {int64(2), nil, int64(3)},
		{int64(3), int64(1), nil}, {int64(3), int64(1), nil}, {int64(3), int64(2), nil}, {int64(3), int64(4), nil}, {int64(3), nil, nil},
	}
}
