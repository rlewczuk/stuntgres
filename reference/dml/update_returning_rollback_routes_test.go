package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningRollback(t *testing.T) {
	for _, savepoint := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			db := backend.DB
			fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,a INT,s TEXT)")
			fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,10,'7'),(2,20,'bad'),(3,30,'9')")
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
			fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
			_, err = tx.Exec("UPDATE t SET a=1")
			fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			if savepoint {
				if _, err = tx.Exec("ROLLBACK TO before_output"); err != nil {
					t.Fatal(err)
				}
				rows, err = tx.Query("SELECT id,a FROM t ORDER BY id")
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "a"}, Types: []string{"INT4", "INT4"}, Rows: [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}, Bag: false})
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
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "a"}, Types: []string{"INT4", "INT4"}, Rows: want, Bag: false})
		}
	}
}
