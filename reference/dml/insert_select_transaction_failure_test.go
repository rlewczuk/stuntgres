package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectTransactionFailure(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(i INT)", 0), fixture.Exec("INSERT INTO t VALUES(9)", 1), fixture.Exec("CREATE TABLE s(i BIGINT)", 0), fixture.Exec("INSERT INTO s VALUES(1),(2147483648)", 2)} {
		p.Step(t, s)
	}
	for _, backend := range p.Backends() {
		tx, err := backend.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, tx, backend.Recorder, s) }
			run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
			failure := fixture.Prepared(fixture.ImplicitError("INSERT INTO t SELECT i FROM s ORDER BY i", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}))
			failure.ErrorStage = "execute"
			run(failure)
			failedBlock := fixture.ImplicitError("SELECT i FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			failedBlock.SelectRows = true
			failedBlock.ErrorStage = "execute"
			run(failedBlock)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
		}()
	}
}
