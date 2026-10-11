package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyRetainedStatements(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT PRIMARY KEY,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'seed')", 1))
		insertQuery, selectQuery, stableQuery := "INSERT INTO t VALUES($1,$2)", "SELECT k FROM t ORDER BY k", "SELECT k::text FROM t ORDER BY k"
		insert, err := a.PrepareContext(p.Ctx, insertQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		selectStmt, err := a.PrepareContext(p.Ctx, selectQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		stable, err := a.PrepareContext(p.Ctx, stableQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stable.Close()
		insertHeld := fixture.HeldStatement{Executor: a, Statement: insert, SQL: insertQuery}
		selectHeld := fixture.HeldStatement{Executor: a, Statement: selectStmt, SQL: selectQuery}
		stableHeld := fixture.HeldStatement{Executor: a, Statement: stable, SQL: stableQuery}
		run(fixture.Exec("ALTER TABLE t ALTER k TYPE BIGINT", 0))
		s := fixture.ImplicitError(selectQuery, fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, selectHeld, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, stableHeld, b.Recorder, fixture.Select(stableQuery, []string{"k"}, []string{"TEXT"}, [][]any{{"1"}}))
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.ErrorDiagnostic(insertQuery, fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (k)=(1) already exists."), int64(1), "bad"))
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insertQuery, 1, int64(2), "valid"))
		run(fixture.Exec("ALTER TABLE t RENAME k TO current_key", 0))
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.ErrorDiagnostic(insertQuery, fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (current_key)=(1) already exists."), int64(1), "bad"))
		run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT t_pkey", 0))
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insertQuery, 1, int64(1), "now allowed"))
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.ErrorDiagnostic(insertQuery, fixture.PrimaryKeyReferenceNull("t", "current_key", "null, bad"), nil, "bad"))
		run(fixture.Select("SELECT * FROM t ORDER BY current_key,v", []string{"current_key", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "now allowed"}, {int64(1), "seed"}, {int64(2), "valid"}}))
		fixture.TableDDLMetadata(t, p.Ctx, a, b.PG, true, "SELECT * FROM t WHERE FALSE", []string{"current_key", "v"}, []string{"INT8", "TEXT"}, []bool{false, true}, 0)
	})
}
