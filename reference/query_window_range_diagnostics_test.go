package reference_test

import (
	"strings"
	"testing"
)

func queryWindowRangeAnalysis(t *testing.T, p *referencePair, b referenceBackend, q, code, message, marker, hint string, args ...any) {
	t.Helper()
	for _, prefix := range []string{"", "/*é*/ "} {
		pos := 0
		if marker != "" {
			i := strings.LastIndex(q, marker)
			if i < 0 {
				t.Fatal("missing marker", q, marker)
			}
			pos = len([]rune(prefix+q[:i])) + 1
		}
		s := referenceImplicitError(prefix+q, referenceDiagnostic{Code: code, Message: message, Position: referencePosition(pos), Hint: hint}, args...)
		s.selectRows = true
		queryWindowRoutes(t, p, b, s)
	}
}

func TestReferenceQueryWindowRangeDiagnostics(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRangeSeed(t, p, b)
		for _, tc := range []struct{ q, code, message, marker, hint string }{
			{"SELECT count(*) OVER(RANGE 1 PRECEDING) FROM range_t", "42P20", "RANGE with offset PRECEDING/FOLLOWING requires exactly one ORDER BY column", "(RANGE", ""},
			{"SELECT count(*) OVER(ORDER BY k,id RANGE 1 PRECEDING) FROM range_t", "42P20", "RANGE with offset PRECEDING/FOLLOWING requires exactly one ORDER BY column", "(ORDER", ""},
			{"SELECT id FROM range_t WINDOW w AS(RANGE 1 PRECEDING)", "42P20", "RANGE with offset PRECEDING/FOLLOWING requires exactly one ORDER BY column", "(RANGE", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE k PRECEDING) FROM range_t", "42P10", "argument of RANGE must not contain variables", "k PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE (SELECT range_t.k) PRECEDING) FROM range_t", "42P10", "argument of RANGE must not contain variables", "range_t.k", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE count(*) PRECEDING) FROM range_t", "42803", "aggregate functions are not allowed in window RANGE", "count(*) PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE grouping(k) PRECEDING) FROM range_t", "42803", "grouping operations are not allowed in window RANGE", "grouping", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE row_number() OVER() PRECEDING) FROM range_t", "42P20", "window functions are not allowed in window definitions", "row_number", ""},
			{"SELECT count(*) OVER(w) FROM range_t WINDOW w AS(ORDER BY k RANGE 1 PRECEDING)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w)", "Omit the parentheses in this OVER clause."},
			{"SELECT count(*) OVER(w RANGE CURRENT ROW) FROM range_t WINDOW w AS(ORDER BY k RANGE CURRENT ROW)", "42P20", "cannot copy window \"w\" because it has a frame clause", "(w RANGE", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE UNBOUNDED FOLLOWING) FROM range_t", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE BETWEEN CURRENT ROW AND UNBOUNDED PRECEDING) FROM range_t", "42P20", "frame end cannot be UNBOUNDED PRECEDING", "UNBOUNDED", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE BETWEEN 1 FOLLOWING AND CURRENT ROW) FROM range_t", "42P20", "frame starting from following row cannot have preceding rows", "CURRENT", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE BETWEEN CURRENT ROW AND 1 PRECEDING) FROM range_t", "42P20", "frame starting from current row cannot have preceding rows", "1 PRECEDING", ""},
			{"SELECT count(*) OVER(ORDER BY k RANGE BETWEEN 1 FOLLOWING AND 1 PRECEDING) FROM range_t", "42P20", "frame starting from following row cannot have preceding rows", "1 PRECEDING", ""},
		} {
			t.Run(tc.q, func(t *testing.T) { queryWindowRangeAnalysis(t, p, b, tc.q, tc.code, tc.message, tc.marker, tc.hint) })
		}
		for _, tc := range []struct{ key, offset, input, source string }{
			{"k", "1.0", "integer", "numeric"}, {"k", "INTERVAL '1 day'", "integer", "interval"}, {"k", "'1'::TEXT", "integer", "text"},
			{"k::DATE", "1", "date", "integer"}, {"k::NUMERIC", "1::DOUBLE PRECISION", "numeric", "double precision"},
		} {
			// DATE has no integer cast: use a real DATE expression rather than k::date.
			key := tc.key
			if key == "k::DATE" {
				key = "DATE '2025-01-01'"
			}
			q := "SELECT count(*) OVER(ORDER BY " + key + " RANGE " + tc.offset + " PRECEDING) FROM range_t"
			marker := tc.offset + " PRECEDING"
			if tc.source == "interval" {
				marker = "'1 day' PRECEDING"
			}
			queryWindowRangeAnalysis(t, p, b, q, "0A000", "RANGE with offset PRECEDING/FOLLOWING is not supported for column type "+tc.input+" and offset type "+tc.source, marker, "Cast the offset value to an appropriate type.")
		}
	})
}

func TestReferenceQueryWindowRangeUnsupportedCarriers(t *testing.T) {
	for _, tc := range []struct{ typ, value, input string }{
		{"TEXT", "'a'", "text"}, {"VARCHAR(3)", "'a'", "text"}, {"CHAR(3)", "'a'", "character"}, {"NAME", "'a'", "name"}, {"\"char\"", "'a'", "\"char\""},
		{"BOOLEAN", "TRUE", "boolean"}, {"BYTEA", "'abc'", "bytea"}, {"MONEY", "'1'", "money"}, {"OID", "1", "oid"},
		{"REGCLASS", "'range_t'", "oid"}, {"REGTYPE", "'integer'", "oid"}, {"UUID", "'00000000-0000-0000-0000-000000000001'", "uuid"},
		{"JSONB", "'1'", "jsonb"}, {"range_enum", "'a'", "anyenum"}, {"range_text", "'a'", "text"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				queryWindowRangeSeed(t, p, b)
				queryWindowRun(t, p, b, referenceExec("CREATE TYPE range_enum AS ENUM('a','b')", 0))
				queryWindowRun(t, p, b, referenceExec("CREATE DOMAIN range_text AS TEXT", 0))
				if tc.typ == `"char"` {
					// CHAR is publicly available from catalog rows; user CHAR DDL stays excluded.
					q := "SELECT count(*) OVER(ORDER BY relkind RANGE 1 PRECEDING) FROM pg_catalog.pg_class"
					queryWindowRangeAnalysis(t, p, b, q, "0A000", `RANGE with offset PRECEDING/FOLLOWING is not supported for column type "char"`, "1 PRECEDING", "")
					return
				}
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE unsupported(k "+tc.typ+")", 0))
				queryWindowRun(t, p, b, referenceExec("INSERT INTO unsupported VALUES("+tc.value+")", 1))
				q := "SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM unsupported"
				queryWindowRangeAnalysis(t, p, b, q, "0A000", "RANGE with offset PRECEDING/FOLLOWING is not supported for column type "+tc.input, "1 PRECEDING", "")
			})
		})
	}
}
