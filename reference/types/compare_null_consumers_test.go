package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompareNullFunctionInsertSelectDiagnostics(t *testing.T) {
	p := fixture.ExpressionReferenceSetup(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE counts(n INT)", 0))
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("INSERT INTO counts SELECT num_nulls(v) FROM t", 4)
			step.Prepared = prepared
			fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, step)
		}
	}
	fixture.ScalarStep(t, p, fixture.Select("SELECT n FROM counts ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(1)}, {int64(1)}}))
	step := fixture.ImplicitError("INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "1"`, Position: "50"})
	step.SelectRows = true
	step.ErrorStage = "execute"
	fixture.ScalarStep(t, p, step)
	step.ErrorStage = "prepare"
	fixture.ScalarStep(t, p, fixture.Prepared(step))
}
