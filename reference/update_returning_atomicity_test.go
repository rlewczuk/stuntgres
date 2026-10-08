//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateReturningOutputAtomicity(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1),(0),(2)", 3)
			for _, prepared := range []bool{false, true} {
				for _, queryRows := range []bool{false, true} {
					s := referenceImplicitError("UPDATE t SET id=id+1 RETURNING 10/old.id", referenceDiagnostic{Code: "22012", Message: "division by zero"})
					s.prepared, s.selectRows, s.errorStage = prepared, queryRows, "execute"
					referenceRunStep(t, p.ctx, db, r, s)
					deleteReferenceRows(t, p, db, r, 0, 1, 2)
				}
			}
			tx, err := db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "INSERT INTO t VALUES(7)", 1)
			s := referenceErrorDiagnostic("UPDATE t SET id=id+1 RETURNING 10/old.id", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			s.selectRows = true
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, tx, r, s)
			s = referenceErrorDiagnostic("SELECT id FROM t", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			s.selectRows = true
			referenceRunStep(t, p.ctx, tx, r, s)
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			deleteReferenceRows(t, p, db, r, 0, 1, 2)
			tx, err = db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "INSERT INTO t VALUES(7)", 1)
			referenceLockExec(t, p.ctx, tx, "SAVEPOINT kept", 0)
			s = referenceErrorDiagnostic("UPDATE t SET id=id+1 RETURNING 10/old.id", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			s.selectRows = true
			referenceRunStep(t, p.ctx, tx, r, s)
			referenceLockExec(t, p.ctx, tx, "ROLLBACK TO SAVEPOINT kept", 0)
			deleteReferenceRows(t, p, tx, r, 0, 1, 2, 7)
			referenceRunStep(t, p.ctx, tx, r, referenceSelect("DELETE FROM t WHERE id=7 RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			deleteReferenceRows(t, p, db, r, 0, 1, 2)
		})
	}
}

func TestReferenceUpdateReturningSequenceDemand(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, db)
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2),(3)", 3)
			referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s START 101 CACHE 1", 0)
			held, err := a.PrepareContext(p.ctx, "UPDATE t SET id=id+1 RETURNING nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT is_called FROM s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			s := referenceSelect("UPDATE t SET id=id+1 RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(101)}, {int64(102)}, {int64(103)}})
			s.bag = true
			referenceRunStep(t, p.ctx, tx, r, s)
			referenceRunStep(t, p.ctx, tx, r, referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(103)}}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			deleteReferenceRows(t, p, a, r, 1, 2, 3)
			referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(104)}}))
			referenceRunStep(t, p.ctx, a, r, referenceSelect("UPDATE t SET id=id+1 WHERE FALSE RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{}))
			referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(104)}}))
			// Failure demands a sequence argument before division, without assuming
			// PostgreSQL row or target-list evaluation order.
			referenceRunStep(t, p.ctx, a, r, referenceImplicitError("UPDATE t SET id=id+1 RETURNING nextval('s')/(id-id)", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			deleteReferenceRows(t, p, a, r, 1, 2, 3)
			var advanced bool
			if err = a.QueryRowContext(p.ctx, "SELECT last_value>104 FROM s").Scan(&advanced); err != nil || !advanced {
				t.Fatal("failed draw rolled back", advanced, err)
			}
		})
	}
}
