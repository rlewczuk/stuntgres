package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningOutputAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		t.Run(backend.Name, func(t *testing.T) {
			r := backend.Recorder
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1),(0),(2)", 3)
			for _, prepared := range []bool{false, true} {
				for _, queryRows := range []bool{false, true} {
					s := fixture.ImplicitError("UPDATE t SET id=id+1 RETURNING 10/old.id", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryRows, "execute"
					fixture.RunStep(t, p.Ctx, db, r, s)
					fixture.DeleteReferenceRows(t, p, db, r, 0, 1, 2)
				}
			}
			tx, err := db.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(7)", 1)
			s := fixture.ErrorDiagnostic("UPDATE t SET id=id+1 RETURNING 10/old.id", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, tx, r, s)
			s = fixture.ErrorDiagnostic("SELECT id FROM t", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, tx, r, s)
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceRows(t, p, db, r, 0, 1, 2)
			tx, err = db.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(7)", 1)
			fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT kept", 0)
			s = fixture.ErrorDiagnostic("UPDATE t SET id=id+1 RETURNING 10/old.id", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, tx, r, s)
			fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO SAVEPOINT kept", 0)
			fixture.DeleteReferenceRows(t, p, tx, r, 0, 1, 2, 7)
			fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("DELETE FROM t WHERE id=7 RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceRows(t, p, db, r, 0, 1, 2)
		})
	}
}

func TestReferenceUpdateReturningSequenceDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		t.Run(backend.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, db)
			r := backend.Recorder
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2),(3)", 3)
			fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE s START 101 CACHE 1", 0)
			held, err := a.PrepareContext(p.Ctx, "UPDATE t SET id=id+1 RETURNING nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, a, r, fixture.Select("SELECT is_called FROM s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			s := fixture.Select("UPDATE t SET id=id+1 RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(101)}, {int64(102)}, {int64(103)}})
			s.Bag = true
			fixture.RunStep(t, p.Ctx, tx, r, s)
			fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(103)}}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceRows(t, p, a, r, 1, 2, 3)
			fixture.RunStep(t, p.Ctx, a, r, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(104)}}))
			fixture.RunStep(t, p.Ctx, a, r, fixture.Select("UPDATE t SET id=id+1 WHERE FALSE RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{}))
			fixture.RunStep(t, p.Ctx, a, r, fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(104)}}))
			// Failure demands a sequence argument before division, without assuming
			// PostgreSQL row or target-list evaluation order.
			fixture.RunStep(t, p.Ctx, a, r, fixture.ImplicitError("UPDATE t SET id=id+1 RETURNING nextval('s')/(id-id)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			fixture.DeleteReferenceRows(t, p, a, r, 1, 2, 3)
			var advanced bool
			if err = a.QueryRowContext(p.Ctx, "SELECT last_value>104 FROM s").Scan(&advanced); err != nil || !advanced {
				t.Fatal("failed draw rolled back", advanced, err)
			}
		})
	}
}
