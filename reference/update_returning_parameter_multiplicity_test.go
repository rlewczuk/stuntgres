package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningParameterMultiplicity(t *testing.T) {
	for _, q := range []string{"UPDATE t SET id=$1 WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated", "UPDATE t SET id=2 RETURNING $1::int4 AS typed,$1 AS repeated"} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
			updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(2),(2)")
			held, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				rows, err := held.Query(2)
				deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"typed", "repeated"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(2), int64(2)}, {int64(2), int64(2)}}, bag: true})
			}
			held.Close()

		}
	}
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,v TEXT)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(2,'2')")
		q := "UPDATE t SET id=$1 WHERE v=$1 RETURNING $1"
		_, err := db.Prepare(q)
		updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"})
		rows, err := db.Query("UPDATE t SET v=$1 WHERE id=$1 RETURNING old.v,new.v,$1", 2)
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"v", "v", "?column?"}, types: []string{"TEXT", "TEXT", "INT4"}, rows: [][]any{{"2", "2", int64(2)}}, bag: false})
	}
}
