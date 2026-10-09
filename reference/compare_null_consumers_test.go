package reference_test

import "testing"

func TestReferenceCompareNullFunctionInsertSelectDiagnostics(t *testing.T) {
	p := expressionReferenceSetup(t)
	scalarStep(t, p, referenceExec("CREATE TABLE counts(n INT)", 0))
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			step := referenceExec("INSERT INTO counts SELECT num_nulls(v) FROM t", 4)
			step.prepared = prepared
			insertSelectRun(t, p.ctx, backend.db, backend.recorder, step)
		}
	}
	scalarStep(t, p, referenceSelect("SELECT n FROM counts ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(1)}, {int64(1)}}))
	step := referenceImplicitError("INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "1"`, Position: "50"})
	step.selectRows = true
	step.errorStage = "execute"
	scalarStep(t, p, step)
	step.errorStage = "prepare"
	scalarStep(t, p, referencePrepared(step))
}
