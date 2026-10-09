package reference_test

import "testing"

func TestReferenceAlterTypeEffectFreePrepareAndCurrentDescriptor(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			utility, err := b.db.PrepareContext(p.ctx, "ALTER TABLE future ALTER c TYPE BIGINT USING c+1")
			if err != nil {
				t.Fatal("utility Prepare resolved absent target", err)
			}
			defer utility.Close()
			run(referenceExec("CREATE TABLE future(c INT DEFAULT 7)", 0))
			run(referenceExec("INSERT INTO future DEFAULT VALUES", 1))
			run(referenceSelect("SELECT c FROM future", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			x := referenceHeldStatement{referenceExecutor: b.db, statement: utility, query: "ALTER TABLE future ALTER c TYPE BIGINT USING c+1"}
			referenceRunStep(t, p.ctx, x, b.recorder, referenceExec(x.query, 0))
			run(referenceSelect("SELECT c FROM future", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
			run(referenceExec("INSERT INTO future DEFAULT VALUES", 1))
			run(referenceSelect("SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(8)}}))
			run(referenceExec(x.query, 0))
			run(referenceSelect("SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}}))
			run(referenceExec("CREATE TABLE p(c INT)", 0))
			failure := referenceImplicitError("ALTER TABLE p ALTER c TYPE INT USING $1", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "38"})
			failure.prepared, failure.errorStage = true, "execute"
			run(failure)
		})
	}
}
