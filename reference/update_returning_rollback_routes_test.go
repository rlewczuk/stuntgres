package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningRollback(t *testing.T) {
	for _, savepoint := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,a INT,s TEXT)")
			updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,10,'7'),(2,20,'bad'),(3,30,'9')")
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, q := range []string{"UPDATE t SET a=11 WHERE id=1", "DELETE FROM t WHERE id=3"} {
				if _, err = tx.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			if savepoint {
				if _, err = tx.Exec("SAVEPOINT before_output"); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := tx.Query("UPDATE t SET a=a+1 RETURNING CAST(s AS INT)")
			if rows != nil {
				rows.Close()
				t.Fatal("failed projection exposed rows")
			}
			updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
			_, err = tx.Exec("UPDATE t SET a=1")
			updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			if savepoint {
				if _, err = tx.Exec("ROLLBACK TO before_output"); err != nil {
					t.Fatal(err)
				}
				rows, err = tx.Query("SELECT id,a FROM t ORDER BY id")
				deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "a"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}, bag: false})
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}
			if savepoint {
				want = [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}
			}
			rows, err = db.Query("SELECT id,a FROM t ORDER BY id")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "a"}, types: []string{"INT4", "INT4"}, rows: want, bag: false})
		}
	}
}
