package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeEffectFreePrepareAndCurrentDescriptorSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			action := "ALTER TABLE future ALTER c TYPE BIGINT USING c+1"
			utility, err := b.DB.Prepare(action)
			if err != nil {
				t.Fatal("utility Prepare resolved absent target", err)
			}
			defer utility.Close()
			run(fixture.Exec("CREATE TABLE future(c INT DEFAULT 7)", 0))
			run(fixture.Exec("INSERT INTO future DEFAULT VALUES", 1))
			run(fixture.Select("SELECT c FROM future", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
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
			run(fixture.Select("SELECT c FROM future", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
			run(fixture.Exec("INSERT INTO future DEFAULT VALUES", 1))
			run(fixture.Select("SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(8)}}))
			run(fixture.Exec(action, 0))
			run(fixture.Select("SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}}))
			run(fixture.Exec("CREATE TABLE p(c INT)", 0))
			failure := fixture.ImplicitError("ALTER TABLE p ALTER c TYPE INT USING $1", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "38"})
			failure.Prepared, failure.ErrorStage = true, "execute"
			run(failure)
		})
	}
}
