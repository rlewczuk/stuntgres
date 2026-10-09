package reference_test

import (
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeEffectFreePrepareAndCurrentDescriptorSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			action := "ALTER TABLE future ALTER c TYPE BIGINT USING c+1"
			utility, err := b.db.Prepare(action)
			if err != nil {
				t.Fatal("utility Prepare resolved absent target", err)
			}
			defer utility.Close()
			run(referenceExec("CREATE TABLE future(c INT DEFAULT 7)", 0))
			run(referenceExec("INSERT INTO future DEFAULT VALUES", 1))
			run(referenceSelect("SELECT c FROM future", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			result, err := utility.Exec()
			if err != nil {
				t.Fatal(err)
			}
			if count, err := result.RowsAffected(); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			if _, err := result.LastInsertId(); err == nil {
				t.Fatal("LastInsertId unexpectedly supported")
			}
			run(referenceSelect("SELECT c FROM future", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
			run(referenceExec("INSERT INTO future DEFAULT VALUES", 1))
			run(referenceSelect("SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(8)}}))
			run(referenceExec(action, 0))
			run(referenceSelect("SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}}))
			run(referenceExec("CREATE TABLE p(c INT)", 0))
			failure := referenceImplicitError("ALTER TABLE p ALTER c TYPE INT USING $1", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "38"})
			failure.prepared, failure.errorStage = true, "execute"
			run(failure)
		})
	}
}
