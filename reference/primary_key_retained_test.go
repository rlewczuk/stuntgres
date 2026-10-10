package reference_test

import "testing"

func TestReferencePrimaryKeyRetainedStatements(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(k INT PRIMARY KEY,v TEXT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'seed')", 1))
		insertQuery, selectQuery, stableQuery := "INSERT INTO t VALUES($1,$2)", "SELECT k FROM t ORDER BY k", "SELECT k::text FROM t ORDER BY k"
		insert, err := a.PrepareContext(p.ctx, insertQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		selectStmt, err := a.PrepareContext(p.ctx, selectQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		stable, err := a.PrepareContext(p.ctx, stableQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stable.Close()
		insertHeld := referenceHeldStatement{referenceExecutor: a, statement: insert, query: insertQuery}
		selectHeld := referenceHeldStatement{referenceExecutor: a, statement: selectStmt, query: selectQuery}
		stableHeld := referenceHeldStatement{referenceExecutor: a, statement: stable, query: stableQuery}
		run(referenceExec("ALTER TABLE t ALTER k TYPE BIGINT", 0))
		s := referenceImplicitError(selectQuery, referenceDiagnostic{Code: "0A000", Message: "cached plan must not change result type"})
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, selectHeld, b.recorder, s)
		referenceRunStep(t, p.ctx, stableHeld, b.recorder, referenceSelect(stableQuery, []string{"k"}, []string{"TEXT"}, [][]any{{"1"}}))
		referenceRunStep(t, p.ctx, insertHeld, b.recorder, referenceErrorDiagnostic(insertQuery, uniqueReferenceDuplicate("t", "t_pkey", "Key (k)=(1) already exists."), int64(1), "bad"))
		referenceRunStep(t, p.ctx, insertHeld, b.recorder, referenceExec(insertQuery, 1, int64(2), "valid"))
		run(referenceExec("ALTER TABLE t RENAME k TO current_key", 0))
		referenceRunStep(t, p.ctx, insertHeld, b.recorder, referenceErrorDiagnostic(insertQuery, uniqueReferenceDuplicate("t", "t_pkey", "Key (current_key)=(1) already exists."), int64(1), "bad"))
		run(referenceExec("ALTER TABLE t DROP CONSTRAINT t_pkey", 0))
		referenceRunStep(t, p.ctx, insertHeld, b.recorder, referenceExec(insertQuery, 1, int64(1), "now allowed"))
		referenceRunStep(t, p.ctx, insertHeld, b.recorder, referenceErrorDiagnostic(insertQuery, primaryKeyReferenceNull("t", "current_key", "null, bad"), nil, "bad"))
		run(referenceSelect("SELECT * FROM t ORDER BY current_key,v", []string{"current_key", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "now allowed"}, {int64(1), "seed"}, {int64(2), "valid"}}))
		referenceTableDDLMetadata(t, p.ctx, a, b.pg, true, "SELECT * FROM t WHERE FALSE", []string{"current_key", "v"}, []string{"INT8", "TEXT"}, []bool{false, true}, 0)
	})
}
