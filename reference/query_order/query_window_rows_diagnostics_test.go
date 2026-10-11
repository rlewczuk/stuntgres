package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independent ROWS diagnostics for pinned-oracle discovery, including
// unknown fractional/overflow input wording and analysis stages.
// The fixture asserts complete logical fields, not merely SQLSTATE/parity.
func TestReferenceQueryWindowRowsDiagnostics(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRowsSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("ALTER TABLE g ADD COLUMN s TEXT", 0))
		for _, tc := range []struct{ q, code, message, marker, hint string }{
			{"SELECT count(*) OVER(ORDER BY k,id ROWS k PRECEDING) FROM g", "42P10", "argument of ROWS must not contain variables", "k PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS (SELECT g.k) PRECEDING) FROM g", "42P10", "argument of ROWS must not contain variables", "g.k", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS s PRECEDING) FROM g", "42804", "argument of ROWS must be type bigint, not type text", "s PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS '1'::TEXT PRECEDING) FROM g", "42804", "argument of ROWS must be type bigint, not type text", "'1'", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS TRUE PRECEDING) FROM g", "42804", "argument of ROWS must be type bigint, not type boolean", "TRUE", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS count(*) PRECEDING) FROM g", "42803", "aggregate functions are not allowed in window ROWS", "count(*) PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS grouping(k) PRECEDING) FROM g", "42803", "grouping operations are not allowed in window ROWS", "grouping", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS row_number() OVER() PRECEDING) FROM g", "42P20", "window functions are not allowed in window definitions", "row_number", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS BETWEEN TRUE PRECEDING AND missing FOLLOWING) FROM g", "42804", "argument of ROWS must be type bigint, not type boolean", "TRUE", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS BETWEEN k PRECEDING AND missing FOLLOWING) FROM g", "42P10", "argument of ROWS must not contain variables", "k PRECEDING", ""},
			{"SELECT count(*) OVER(w) FROM g WINDOW w AS(ORDER BY k,id ROWS 1 PRECEDING)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w)", "Omit the parentheses in this OVER clause."},
			{"SELECT count(*) OVER(w ROWS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k,id ROWS CURRENT ROW)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w ROWS", ""},
			{"SELECT count(*) OVER v FROM g WINDOW w AS(ORDER BY k,id ROWS CURRENT ROW),v AS(w)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w)", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS UNBOUNDED FOLLOWING) FROM g", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ROWS BETWEEN CURRENT ROW AND (+1) PRECEDING) FROM g", "42P20", "frame starting from current row cannot have preceding rows", "(+1)", ""},
			{"SELECT count(*) OVER(ROWS +1 FOLLOWING) FROM g", "42P20", "frame starting from following row cannot end with current row", "+1", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS 1 FOLLOWING) FROM g", "42P20", "frame starting from following row cannot end with current row", "1 FOLLOWING", ""},
			{"SELECT count(*) OVER(ORDER BY missing ROWS UNBOUNDED FOLLOWING) FROM missing_table", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS BETWEEN CURRENT ROW AND UNBOUNDED PRECEDING) FROM g", "42P20", "frame end cannot be UNBOUNDED PRECEDING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS BETWEEN CURRENT ROW AND 1 PRECEDING) FROM g", "42P20", "frame starting from current row cannot have preceding rows", "1 PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS BETWEEN 1 FOLLOWING AND CURRENT ROW) FROM g", "42P20", "frame starting from following row cannot have preceding rows", "CURRENT", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS BETWEEN 1 FOLLOWING AND 1 PRECEDING) FROM g", "42P20", "frame starting from following row cannot have preceding rows", "1 PRECEDING", ""},
			{"SELECT id FROM g WINDOW unused AS(ORDER BY k,id ROWS TRUE PRECEDING)", "42804", "argument of ROWS must be type bigint, not type boolean", "TRUE", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS '1.5' PRECEDING) FROM g", "22P02", "invalid input syntax for type bigint: \"1.5\"", "'1.5'", ""},
			{"SELECT count(*) OVER(ORDER BY k,id ROWS '9223372036854775808' PRECEDING) FROM g", "22003", "value \"9223372036854775808\" is out of range for type bigint", "'9223372036854775808'", ""},
		} {
			t.Run(tc.q, func(t *testing.T) { queryWindowRangeAnalysis(t, p, b, tc.q, tc.code, tc.message, tc.marker, tc.hint) })
		}
	})
}

func TestReferenceQueryWindowRowsNamed(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRowsSeed(t, p, b)
		for _, over := range []string{"w", "(base ROWS 1 PRECEDING)"} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER "+over+" AS n FROM g WINDOW base AS(ORDER BY k,id),w AS(base ROWS 1 PRECEDING) ORDER BY id", 1, 2, 2, 2, 2))
		}
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(w ORDER BY k,id ROWS 1 PRECEDING) AS n FROM g WINDOW w AS() ORDER BY id", 1, 2, 2, 2, 2))
		for _, tc := range []struct{ q, code, message, marker string }{
			{"SELECT count(*) OVER w FROM g WINDOW w AS(later ROWS CURRENT ROW),later AS(ORDER BY k)", "42704", "window \"later\" does not exist", "(later ROWS"},
			{"SELECT count(*) OVER(w ORDER BY k,id ROWS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k,id ROWS CURRENT ROW)", "42P20", "cannot override ORDER BY clause of window \"w\"", "(w ORDER"},
			{"SELECT count(*) OVER(w ORDER BY missing ROWS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k,id ROWS CURRENT ROW)", "42703", "column \"missing\" does not exist", "missing"},
			{"SELECT count(*) OVER(w PARTITION BY k ROWS CURRENT ROW) FROM g WINDOW w AS(ORDER BY k)", "42P20", "cannot override PARTITION BY clause of window \"w\"", "(w PARTITION"},
		} {
			queryWindowRangeAnalysis(t, p, b, tc.q, tc.code, tc.message, tc.marker, "")
		}
	})
}
