package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceGroupingSetsDiagnostics(t *testing.T) {
	const mismatch = "arguments to GROUPING must be grouping expressions of the associated query level"
	cases := []struct{ q, code, message, token string }{
		{"SELECT GROUPING(a) FROM gs GROUP BY a+1", "42803", mismatch, "a)"},
		{"/* é界 */ SELECT GROUPING(a+1) FROM gs GROUP BY a", "42803", mismatch, "a+1"},
		{"SELECT GROUPING(b) FROM gs_empty GROUP BY a HAVING FALSE", "42803", mismatch, "b)"},
		{"SELECT GROUPING(missing) FROM gs GROUP BY a", "42703", "column \"missing\" does not exist", "missing"},
		{"SELECT a FROM gs WHERE GROUPING(a)=0 GROUP BY a", "42803", "grouping operations are not allowed in WHERE", "GROUPING"},
		{"SELECT count(*) FROM gs GROUP BY GROUPING(a)", "42803", "grouping operations are not allowed in GROUP BY", "GROUPING"},
		{"SELECT count(*) FILTER(WHERE GROUPING(a)=0) FROM gs GROUP BY a", "42803", "grouping operations are not allowed in FILTER", "GROUPING"},
		{"SELECT count(*) FROM gs x JOIN gs y ON GROUPING(x.a)=0 GROUP BY x.a", "42803", "grouping operations are not allowed in JOIN conditions", "GROUPING"},
		{"SELECT a FROM gs GROUP BY a LIMIT GROUPING(a)", "42803", "grouping operations are not allowed in LIMIT", "GROUPING"},
		{"SELECT a FROM gs GROUP BY a OFFSET GROUPING(a)", "42803", "grouping operations are not allowed in OFFSET", "GROUPING"},
		{"INSERT INTO gs VALUES(GROUPING(1),1,1)", "42803", "grouping operations are not allowed in VALUES", "GROUPING"},
		{"UPDATE gs SET a=GROUPING(a)", "42803", "grouping operations are not allowed in UPDATE", "GROUPING"},
		{"UPDATE gs SET a=a RETURNING GROUPING(a)", "42803", "grouping operations are not allowed in RETURNING", "GROUPING"},
		{"SELECT sum(GROUPING(a)) FROM gs GROUP BY a", "42803", "aggregate function calls cannot be nested", "GROUPING"},
		{"SELECT GROUPING(sum(a)) FROM gs GROUP BY a", "42803", "aggregate function calls cannot be nested", "sum(a)"},
		{"SELECT GROUPING(GROUPING(a)) FROM gs GROUP BY a", "42803", "aggregate function calls cannot be nested", "GROUPING(a)"},
		{"SELECT GROUPING(row_number() OVER ()) FROM gs GROUP BY a", "42803", "aggregate function calls cannot contain window function calls", "row_number"},
		{"SELECT GROUPING() FROM gs", "42601", "syntax error at or near \")\"", ")"},
		{"SELECT count(*) FROM gs GROUP BY ROLLUP()", "42601", "syntax error at or near \")\"", ")"},
		{"SELECT count(*) FROM gs GROUP BY CUBE()", "42601", "syntax error at or near \")\"", ")"},
		{"SELECT count(*) FROM gs GROUP BY GROUPING SETS()", "42601", "syntax error at or near \")\"", ")"},
		{"SELECT GROUPING(a) FILTER(WHERE TRUE) FROM gs GROUP BY a", "42601", "syntax error at or near \"FILTER\"", "FILTER"},
		{"SELECT GROUPING(a) OVER () FROM gs GROUP BY a", "42601", "syntax error at or near \"OVER\"", "OVER"},
		{"SELECT a FROM gs GROUP BY ()", "42803", "column \"gs.a\" must appear in the GROUP BY clause or be used in an aggregate function", "a FROM"},
		{"SELECT count(*) FROM gs GROUP BY () FOR UPDATE", "0A000", "FOR UPDATE is not allowed with GROUP BY clause", ""},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
				groupingSetsFixture(run)
				s := groupingSetsError(tc.q, tc.code, tc.message, tc.token)
				// Only malformed empty construct calls point at the final close;
				// legal GROUP BY () with an ungrouped projection points at a (8).
				if tc.code == "42601" && strings.HasSuffix(tc.q, "()") {
					s.diagnostic.Position = referencePosition(len(tc.q))
				}
				groupingSetsAnalysisError(run, s)
				run(referenceSelect("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
			})
		})
	}
}

func TestReferenceGroupingSetsLimits(t *testing.T) {
	args := func(n int) string { return strings.TrimSuffix(strings.Repeat("a,", n), ",") }
	empties := func(n int) string { return strings.TrimSuffix(strings.Repeat("(),", n), ",") }
	for _, tc := range []struct {
		name, clause string
		rows         int
	}{
		{"CubeTwelve", "CUBE(" + args(12) + ")", 4096},
		{"CubeTwelveDistinct", "DISTINCT CUBE(" + args(12) + ")", 2},
		{"Empty4096", "GROUPING SETS(" + empties(4096) + ")", 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE gs(a INT)", 0))
				// One source row gives one group for every cube subset, including empty.
				if tc.name != "Empty4096" {
					run(referenceExec("INSERT INTO gs VALUES(1)", 1))
				}
				rows := make([][]any, tc.rows)
				var count int64
				if tc.name != "Empty4096" {
					count = 1
				}
				for i := range rows {
					rows[i] = []any{count}
				}
				groupingSetsBoth(run, groupingSetsBag("SELECT count(*) FROM gs GROUP BY "+tc.clause, []string{"count"}, []string{"INT8"}, rows))
			})
		})
	}
	cases := []struct{ name, q, code, message, token string }{
		{"Grouping32", "SELECT GROUPING(" + args(32) + ") FROM gs GROUP BY a", "54023", "GROUPING must have fewer than 32 arguments", "GROUPING"},
		{"Grouping32BeforeChildren", "SELECT GROUPING(missing," + args(31) + ") FROM gs GROUP BY a", "54023", "GROUPING must have fewer than 32 arguments", "GROUPING"},
		{"Cube13", "SELECT count(*) FROM gs GROUP BY CUBE(" + args(13) + ")", "54011", "CUBE is limited to 12 elements", "CUBE"},
		{"Cube13Distinct", "SELECT count(*) FROM gs GROUP BY DISTINCT CUBE(" + args(13) + ")", "54011", "CUBE is limited to 12 elements", "CUBE"},
		{"CubeMissingPrecedence", "SELECT count(*) FROM gs GROUP BY CUBE(missing," + args(12) + ")", "42703", "column \"missing\" does not exist", "missing"},
		{"Empty4097", "SELECT count(*) FROM gs GROUP BY GROUPING SETS(" + empties(4097) + ")", "54001", "too many grouping sets present (maximum 4096)", "GROUPING SETS"},
		{"Empty4097Distinct", "SELECT count(*) FROM gs GROUP BY DISTINCT GROUPING SETS(" + empties(4097) + ")", "54001", "too many grouping sets present (maximum 4096)", "GROUPING SETS"},
		{"Product8192", "SELECT count(*) FROM gs GROUP BY CUBE(" + args(12) + "),CUBE(a)", "54001", "too many grouping sets present (maximum 4096)", ""},
		{"Product8192Distinct", "SELECT count(*) FROM gs GROUP BY DISTINCT CUBE(" + args(12) + "),CUBE(a)", "54001", "too many grouping sets present (maximum 4096)", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE gs(a INT)", 0))
				groupingSetsAnalysisError(run, groupingSetsError(tc.q, tc.code, tc.message, tc.token))
			})
		})
	}
}
