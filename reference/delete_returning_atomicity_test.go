//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteReturningOutputAtomicity(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1),(0),(2)", 3)
			for _, prepared := range []bool{false, true} {
				for _, queryRows := range []bool{false, true} {
					s := referenceImplicitError("DELETE FROM t RETURNING 10/id", referenceDiagnostic{Code: "22012", Message: "division by zero"})
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
			s := referenceErrorDiagnostic("DELETE FROM t RETURNING 10/id", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
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
			s = referenceErrorDiagnostic("DELETE FROM t RETURNING 10/id", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
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

func TestReferenceDeleteReturningSequenceDemand(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, db)
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2),(3)", 3)
			referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s START 101 CACHE 1", 0)
			held, err := a.PrepareContext(p.ctx, "DELETE FROM t RETURNING nextval('s')")
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
			s := referenceSelect("DELETE FROM t RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(101)}, {int64(102)}, {int64(103)}})
			s.bag = true
			referenceRunStep(t, p.ctx, tx, r, s)
			referenceRunStep(t, p.ctx, tx, r, referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(103)}}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			deleteReferenceRows(t, p, a, r, 1, 2, 3)
			referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(104)}}))
			referenceRunStep(t, p.ctx, a, r, referenceSelect("DELETE FROM t WHERE FALSE RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{}))
			referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(104)}}))
			// Failure demands a sequence argument before division, without assuming
			// PostgreSQL row or target-list evaluation order.
			referenceRunStep(t, p.ctx, a, r, referenceImplicitError("DELETE FROM t RETURNING nextval('s')/(id-id)", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			deleteReferenceRows(t, p, a, r, 1, 2, 3)
			var advanced bool
			if err = a.QueryRowContext(p.ctx, "SELECT last_value>104 FROM s").Scan(&advanced); err != nil || !advanced {
				t.Fatal("failed draw rolled back", advanced, err)
			}
		})
	}
}

func TestReferenceDeleteReturningBoundaries(t *testing.T) {
	p := newReferencePair(t)
	// These are PostgreSQL controls only; records, scalar subqueries, SRFs,
	// window execution, triggers, FKs and rewriting are not native providers.
	t.Run("postgres", func(t *testing.T) {
		db, r := p.postgres, p.postgresRecord
		referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT PRIMARY KEY)", 0)
		referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1)", 1)
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM t RETURNING (SELECT 7) AS scalar,old::text AS predecessor,new::text AS replacement", []string{"scalar", "predecessor", "replacement"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "(1)", nil}}))
		referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1)", 1)
		// PG18.0's parenthesized whole-row expansion preserves target OLD
		// fields here; this excluded record path is not ordinary new.id/new.*.
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM t RETURNING (old).*,(new).*", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		for _, tc := range []struct{ q, code, message string }{
			{"DELETE FROM t RETURNING row_number() OVER ()", "42P20", "window functions are not allowed in RETURNING"},
			{"DELETE FROM t RETURNING generate_series(1,2)", "0A000", "set-returning functions are not allowed in RETURNING"},
		} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: "25"})
			s.selectRows = true
			referenceRunStep(t, p.ctx, db, r, s)
		}
		referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1)", 1)
		referenceLockExec(t, p.ctx, db, "CREATE TABLE child(id INT CONSTRAINT child_ref REFERENCES t(id))", 0)
		referenceLockExec(t, p.ctx, db, "INSERT INTO child VALUES(1)", 1)
		s := referenceImplicitError("DELETE FROM t RETURNING id", referenceDiagnostic{Code: "23503", Message: `update or delete on table "t" violates foreign key constraint "child_ref" on table "child"`, Detail: `Key (id)=(1) is still referenced from table "child".`, Schema: "public", Table: "child", Constraint: "child_ref"})
		s.selectRows = true
		referenceRunStep(t, p.ctx, db, r, s)
		deleteReferenceRows(t, p, db, r, 1)
		referenceLockExec(t, p.ctx, db, "CREATE FUNCTION suppress_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$", 0)
		referenceLockExec(t, p.ctx, db, "CREATE TRIGGER suppress BEFORE DELETE ON t FOR EACH ROW EXECUTE FUNCTION suppress_delete()", 0)
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM t RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{}))
		deleteReferenceRows(t, p, db, r, 1)
		referenceLockExec(t, p.ctx, db, "CREATE TABLE rewritten(id INT)", 0)
		referenceLockExec(t, p.ctx, db, "INSERT INTO rewritten VALUES(1)", 1)
		referenceLockExec(t, p.ctx, db, "CREATE RULE rewrite_delete AS ON DELETE TO rewritten DO INSTEAD UPDATE rewritten SET id=old.id+10 WHERE id=old.id RETURNING rewritten.*", 0)
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM rewritten RETURNING old.id,new.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
		referenceRunStep(t, p.ctx, db, r, referenceSelect("SELECT id FROM rewritten", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
	})
}
