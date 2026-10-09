package reference_test

import (
	"database/sql"
	"testing"
)

// Every observation is now a permanent literal assertion shared by both modes.
func outerJoinsAssert(t *testing.T, p *referencePair, x referenceExecutor, recorder *referenceRecorder, query string, prepared bool) bool {
	t.Helper()
	if want, ok := outerJoinsDiagnostics[query]; ok {
		step := referenceErrorDiagnostic(query, want)
		step.prepared, step.selectRows = prepared, true
		step.errorStage = "execute"
		if prepared {
			step.errorStage = outerJoinsPreparedErrorStages[query]
		}
		referenceRunStep(t, p.ctx, x, recorder, step)
		return false
	}
	step, ok := outerJoinsResults[query]
	if !ok {
		t.Fatalf("missing independent outer join expectation: %s", query)
	}
	step.prepared, step.bag = prepared, true
	referenceRunStep(t, p.ctx, x, recorder, step)
	return true
}

func TestReferenceQueryOuterJoinsDiagnostics(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE text_key(k TEXT)", 0), referenceExec("CREATE TABLE only_right(r INTEGER)", 0)} {
			referenceRunStep(t, p.ctx, conn, recorder, s)
		}
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, operands := range []string{"a " + kind + " JOIN b", "empty_a a " + kind + " JOIN empty_b b"} {
				for _, predicate := range []string{"1", "count(*)>0", "a.absent=b.k", "zz.k=b.k", "k=1", "a.k=b.r"} {
					for _, prefix := range []string{"", "/*é*/ "} {
						for _, suffix := range []string{"", " LIMIT 0"} {
							query := prefix + "SELECT * FROM " + operands + " ON " + predicate + suffix
							for _, prepared := range []bool{false, true} {
								if outerJoinsAssert(t, p, conn, recorder, query, prepared) {
									t.Errorf("invalid predicate succeeded: %s", query)
								}
							}
						}
					}
				}
			}
			for _, query := range []string{
				"SELECT * FROM a " + kind + " JOIN b USING(k,k)",
				"SELECT * FROM a " + kind + " JOIN b USING(absent)",
				"SELECT * FROM a " + kind + " JOIN only_right USING(k)",
				"SELECT * FROM (a CROSS JOIN b) " + kind + " JOIN empty_a USING(k)",
				"SELECT * FROM empty_a " + kind + " JOIN (a CROSS JOIN b) USING(k)",
				"SELECT * FROM a " + kind + " JOIN text_key USING(k)",
				"SELECT * FROM a, b " + kind + " JOIN empty_a c ON a.k=c.k",
				"SELECT * FROM a NATURAL " + kind + " JOIN b ON TRUE",
				"SELECT * FROM a NATURAL " + kind + " JOIN b USING(k)",
				"SELECT * FROM a " + kind + " JOIN b",
				"SELECT * FROM a " + kind + " JOIN b ON",
				"SELECT a.k FROM (a " + kind + " JOIN b USING(k)) j",
				"SELECT u.l FROM a " + kind + " JOIN b USING(k) AS u",
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					for _, prepared := range []bool{false, true} {
						if outerJoinsAssert(t, p, conn, recorder, prefix+query, prepared) {
							t.Errorf("invalid join succeeded: %s", query)
						}
					}
				}
			}
		}
		for _, query := range []string{"SELECT * FROM a OUTER JOIN b ON TRUE", "SELECT * FROM a CROSS OUTER JOIN b", "SELECT * FROM a LEFT OUTER OUTER JOIN b ON TRUE"} {
			for _, prepared := range []bool{false, true} {
				if outerJoinsAssert(t, p, conn, recorder, query, prepared) {
					t.Errorf("invalid syntax succeeded: %s", query)
				}
			}
		}
	})
}

func TestReferenceQueryOuterJoinsFullLegality(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, operands := range []string{"a FULL JOIN b", "empty_a a FULL JOIN b", "a FULL JOIN empty_b b", "empty_a a FULL JOIN empty_b b"} {
			for _, predicate := range []string{
				"TRUE", "FALSE", "NULL::boolean", "a.k=b.k", "a.k<b.k", "a.k=a.k",
				"a.k=b.k AND a.l='a'", "a.k=b.k AND a.k<b.k", "a.k=b.k OR a.l='a'",
				"FALSE AND a.k<b.k", "TRUE OR a.k<b.k", "a.k=b.k AND TRUE", "a.k=b.k OR FALSE",
				"1", "a.k=b.r", "count(*)>0",
			} {
				for _, suffix := range []string{"", " LIMIT 0"} {
					for _, prepared := range []bool{false, true} {
						outerJoinsAssert(t, p, conn, recorder, "SELECT * FROM "+operands+" ON "+predicate+suffix, prepared)
					}
				}
			}
		}
	})
}
