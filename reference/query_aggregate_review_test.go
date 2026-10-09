package reference_test

import "testing"

func TestReferenceQueryAggregateReviewRegression(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(v NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(2)", 2))
	invalid := referenceImplicitError("SELECT v=1.0,count(*) FROM t GROUP BY v=1.00", referenceDiagnostic{
		Code: "42803", Message: `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8",
	})
	invalid.selectRows = true
	invalid.errorStage = "execute"
	control := referenceSelect("SELECT v=1.0,count(*) FROM t GROUP BY v=1.0", []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}, {false, int64(1)}})
	control.bag = true
	pgSibling := referenceSelect("SELECT count(*),CAST('0' AS regclass)", []string{"count", "regclass"}, []string{"INT8", "REGCLASS"}, [][]any{{int64(1), "-"}})

	if p.postgres != nil {
		t.Run("postgres", func(t *testing.T) {
			for _, step := range []referenceStep{invalid, control, pgSibling} {
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
				if step.diagnostic != nil {
					step.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(step))
			}
		})
	}
	t.Run("native", func(t *testing.T) {
		for _, step := range []referenceStep{invalid, control} {
			t.Run(step.query, func(t *testing.T) {
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
				if step.diagnostic != nil {
					step.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(step))
			})
		}
		t.Run("no prefix effects", func(t *testing.T) {
			failure := referenceError("INSERT INTO t VALUES(3);SELECT count(*) WHERE", "42601")
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
			queryAggregatePrepareRejection(t, p, failure.query, failure.state)
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	})
	p.step(t, pgSibling)
	p.step(t, referencePrepared(pgSibling))
}
