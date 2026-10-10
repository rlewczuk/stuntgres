package reference_test

import "testing"

func TestReferenceQueryWindowRangeOrderingAdmission(t *testing.T) {
	for _, tc := range []struct{ typ, value string }{{"integer[]", "'{1}'"}, {"json", "'1'"}, {"jsonpath", "'$'"}} {
		t.Run(tc.typ, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE unsortable(k "+tc.typ+")", 0))
				queryWindowRun(t, p, b, referenceExec("INSERT INTO unsortable VALUES("+tc.value+")", 1))
				q := "SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM unsortable"
				if b.pg && tc.typ == "integer[]" {
					queryWindowRangeAnalysis(t, p, b, q, "0A000", "RANGE with offset PRECEDING/FOLLOWING is not supported for column type anyarray", "1 PRECEDING", "")
				} else {
					queryWindowRangeAnalysis(t, p, b, q, "42883", "could not identify an ordering operator for type "+tc.typ, "k RANGE", "Use an explicit ordering operator or modify the query.")
				}
			})
		})
	}
}

func TestReferenceQueryWindowRangeParameterDiagnostics(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRangeSeed(t, p, b)
		// Unknown receives the support's input context, not assignment conversion.
		for _, q := range []string{
			"SELECT id,count(*) OVER(ORDER BY k RANGE $1 PRECEDING) AS n FROM range_t ORDER BY id",
			"SELECT id,count(*) OVER(ORDER BY k RANGE $1::BIGINT PRECEDING) AS n FROM range_t ORDER BY id",
		} {
			s := queryWindowRangeResult(q, 1, 3, 3, 1, 2, 2, 2)
			s.args = []any{1}
			queryWindowRoutes(t, p, b, s)
		}
		q := "SELECT count(*) OVER(ORDER BY k RANGE $1::TEXT PRECEDING) FROM range_t"
		queryWindowRangeAnalysis(t, p, b, q, "0A000", "RANGE with offset PRECEDING/FOLLOWING is not supported for column type integer and offset type text", "$1::TEXT", "Cast the offset value to an appropriate type.", "1")
	})
}
