package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independently specified complete diagnostics, confirmed by pinned PostgreSQL
// discovery, including unknown fractional/overflow input wording and stages.
// The fixture asserts complete logical fields, not merely SQLSTATE/parity.
func TestReferenceQueryWindowGroupsDiagnostics(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("ALTER TABLE g ADD COLUMN s TEXT", 0))
		for _, tc := range []struct{ q, code, message, marker, hint string }{
			{"SELECT count(*) OVER(GROUPS 1 PRECEDING) FROM g", "42P20", "GROUPS mode requires an ORDER BY clause", "(GROUPS", ""},
			{"SELECT count(*) OVER(GROUPS CURRENT ROW) FROM g", "42P20", "GROUPS mode requires an ORDER BY clause", "(GROUPS", ""},
			{"SELECT count(*) OVER(GROUPS UNBOUNDED PRECEDING) FROM g", "42P20", "GROUPS mode requires an ORDER BY clause", "(GROUPS", ""},
			{"SELECT id FROM g WINDOW w AS(GROUPS CURRENT ROW)", "42P20", "GROUPS mode requires an ORDER BY clause", "(GROUPS", ""},
			{"SELECT count(*) OVER(GROUPS missing PRECEDING) FROM g", "42P20", "GROUPS mode requires an ORDER BY clause", "(GROUPS", ""},
			{"SELECT count(*) OVER(GROUPS row_number() OVER() PRECEDING) FROM g", "42P20", "GROUPS mode requires an ORDER BY clause", "(GROUPS", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS k PRECEDING) FROM g", "42P10", "argument of GROUPS must not contain variables", "k PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS (SELECT g.k) PRECEDING) FROM g", "42P10", "argument of GROUPS must not contain variables", "g.k", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS s PRECEDING) FROM g", "42804", "argument of GROUPS must be type bigint, not type text", "s PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS '1'::TEXT PRECEDING) FROM g", "42804", "argument of GROUPS must be type bigint, not type text", "'1'", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS TRUE PRECEDING) FROM g", "42804", "argument of GROUPS must be type bigint, not type boolean", "TRUE", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS count(*) PRECEDING) FROM g", "42803", "aggregate functions are not allowed in window GROUPS", "count(*) PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS grouping(k) PRECEDING) FROM g", "42803", "grouping operations are not allowed in window GROUPS", "grouping", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS row_number() OVER() PRECEDING) FROM g", "42P20", "window functions are not allowed in window definitions", "row_number", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN TRUE PRECEDING AND missing FOLLOWING) FROM g", "42804", "argument of GROUPS must be type bigint, not type boolean", "TRUE", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN k PRECEDING AND missing FOLLOWING) FROM g", "42P10", "argument of GROUPS must not contain variables", "k PRECEDING", ""},
			{"SELECT count(*) OVER(w) FROM g WINDOW w AS(ORDER BY k GROUPS 1 PRECEDING)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w)", "Omit the parentheses in this OVER clause."},
			{"SELECT count(*) OVER(w GROUPS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k GROUPS CURRENT ROW)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w GROUPS", ""},
			{"SELECT count(*) OVER v FROM g WINDOW w AS(ORDER BY k GROUPS CURRENT ROW),v AS(w)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w)", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS UNBOUNDED FOLLOWING) FROM g", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS 1 FOLLOWING) FROM g", "42P20", "frame starting from following row cannot end with current row", "1 FOLLOWING", ""},
			{"SELECT count(*) OVER(ORDER BY missing GROUPS UNBOUNDED FOLLOWING) FROM missing_table", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN CURRENT ROW AND UNBOUNDED PRECEDING) FROM g", "42P20", "frame end cannot be UNBOUNDED PRECEDING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN CURRENT ROW AND 1 PRECEDING) FROM g", "42P20", "frame starting from current row cannot have preceding rows", "1 PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN 1 FOLLOWING AND CURRENT ROW) FROM g", "42P20", "frame starting from following row cannot have preceding rows", "CURRENT", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN 1 FOLLOWING AND 1 PRECEDING) FROM g", "42P20", "frame starting from following row cannot have preceding rows", "1 PRECEDING", ""},
			{"SELECT id FROM g WINDOW unused AS(ORDER BY k GROUPS TRUE PRECEDING)", "42804", "argument of GROUPS must be type bigint, not type boolean", "TRUE", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS '1.5' PRECEDING) FROM g", "22P02", "invalid input syntax for type bigint: \"1.5\"", "'1.5'", ""},
			{"SELECT count(*) OVER(ORDER BY k GROUPS '9223372036854775808' PRECEDING) FROM g", "22003", "value \"9223372036854775808\" is out of range for type bigint", "'9223372036854775808'", ""},
		} {
			t.Run(tc.q, func(t *testing.T) { queryWindowRangeAnalysis(t, p, b, tc.q, tc.code, tc.message, tc.marker, tc.hint) })
		}
	})
}

func TestReferenceQueryWindowGroupsNamed(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		for _, over := range []string{"w", "(base GROUPS 1 PRECEDING)"} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER "+over+" AS n FROM g WINDOW base AS(ORDER BY k),w AS(base GROUPS 1 PRECEDING) ORDER BY id", 2, 2, 3, 3, 3))
		}
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(w ORDER BY k GROUPS 1 PRECEDING) AS n FROM g WINDOW w AS() ORDER BY id", 2, 2, 3, 3, 3))
		for _, tc := range []struct{ q, code, message, marker string }{
			{"SELECT count(*) OVER w FROM g WINDOW w AS(later GROUPS CURRENT ROW),later AS(ORDER BY k)", "42704", "window \"later\" does not exist", "(later GROUPS"},
			{"SELECT count(*) OVER(w ORDER BY k GROUPS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k GROUPS CURRENT ROW)", "42P20", "cannot override ORDER BY clause of window \"w\"", "(w ORDER"},
			{"SELECT count(*) OVER(w ORDER BY missing GROUPS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k GROUPS CURRENT ROW)", "42703", "column \"missing\" does not exist", "missing"},
			{"SELECT count(*) OVER(w PARTITION BY k GROUPS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k)", "42P20", "cannot override PARTITION BY clause of window \"w\"", "(w PARTITION"},
		} {
			queryWindowRangeAnalysis(t, p, b, tc.q, tc.code, tc.message, tc.marker, "")
		}
	})
}
