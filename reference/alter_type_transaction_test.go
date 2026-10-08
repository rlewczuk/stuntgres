//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterTypeTransactionsAndLiveRows(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE t(id INT,c TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'oops'),(2,'bad'),(3,'9')", 3))
			// Ended versions must not be converted, even when their payload is invalid.
			run(referenceExec("UPDATE t SET c='7' WHERE id=1", 1))
			run(referenceExec("DELETE FROM t WHERE id=2", 1))
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.record, s) }
			tr(referenceExec("INSERT INTO t VALUES(4,'ownbad')", 1))
			tr(referenceExec("UPDATE t SET c='8' WHERE id=4", 1))
			tr(referenceExec("INSERT INTO t VALUES(5,'deletedbad')", 1))
			tr(referenceExec("DELETE FROM t WHERE id=5", 1))
			tr(referenceExec("SAVEPOINT s", 0))
			tr(referenceExec("ALTER TABLE t ALTER c TYPE INT USING c::int", 0))
			tr(referenceSelect("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(8)}}))
			tr(referenceExec("ROLLBACK TO s", 0))
			tr(referenceExec("ROLLBACK TO s", 0))
			tr(referenceSelect("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}, {"9"}, {"8"}}))
			tr(referenceExec("INSERT INTO t VALUES(6,'latebad')", 1))
			tr(referenceImplicitError("ALTER TABLE t ALTER c TYPE INT USING c::int", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "latebad"`}))
			tr(referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			tr(referenceExec("ROLLBACK TO s", 0))
			tr(referenceExec("ALTER TABLE t ALTER c TYPE INT USING c::int", 0))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(8)}}))
			ro, err := b.db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, ro, b.record, referenceImplicitError("ALTER TABLE absent ALTER c TYPE BIGINT", referenceDiagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
			if err = ro.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceAlterTypePreparedMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE t(id INT,c INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,7)", 1))
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			held, err := conn.PrepareContext(p.ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			stable, err := conn.PrepareContext(p.ctx, "SELECT c::text FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer stable.Close()
			referenceRunStep(t, p.ctx, conn, b.record, referenceExec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
			rows, err := held.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
			if got := referenceExtractDiagnostic(t, err); got != want {
				t.Fatal(got, want)
			}
			rows, err = stable.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("missing stable row")
			}
			var value string
			if err = rows.Scan(&value); err != nil || value != "8" {
				t.Fatal(value, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra row", rows.Err())
			}
			rows.Close()
			referenceRunStep(t, p.ctx, conn, b.record, referenceSelect("SELECT c FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
		})
	}
}
