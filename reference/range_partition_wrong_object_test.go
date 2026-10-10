package reference_test

import "testing"

func TestReferenceRangePartitionWrongObjectDiagnostic(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(step referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, step) }
		run(referenceExec("CREATE TABLE rp_wrong_root(k INT) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_wrong_leaf PARTITION OF rp_wrong_root DEFAULT", 0))
		run(referenceExec("INSERT INTO rp_wrong_root VALUES(1)", 1))
		for _, prepared := range []bool{false, true} {
			for _, name := range []string{"rp_wrong_root", "rp_wrong_leaf"} {
				step := referenceRangeError("DROP INDEX "+name, "42809", `"`+name+`" is not an index`, referenceDiagnostic{Hint: "Use DROP TABLE to remove a table."})
				if prepared {
					step = referencePrepared(step)
				}
				run(step)
			}
			step := referenceSelect("SELECT k FROM rp_wrong_root", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})
			if prepared {
				step = referencePrepared(step)
			}
			run(step)
		}
	})
}
