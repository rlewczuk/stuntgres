package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningParameterMultiplicity(t *testing.T) {
	for _, q := range []string{"UPDATE t SET id=$1 WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated", "UPDATE t SET id=2 RETURNING $1::int4 AS typed,$1 AS repeated"} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			db := backend.DB
			fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
			fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(2),(2)")
			held, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				rows, err := held.Query(2)
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"typed", "repeated"}, Types: []string{"INT4", "INT4"}, Rows: [][]any{{int64(2), int64(2)}, {int64(2), int64(2)}}, Bag: true})
			}
			held.Close()

		}
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,v TEXT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(2,'2')")
		q := "UPDATE t SET id=$1 WHERE v=$1 RETURNING $1"
		_, err := db.Prepare(q)
		fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"})
		rows, err := db.Query("UPDATE t SET v=$1 WHERE id=$1 RETURNING old.v,new.v,$1", 2)
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"v", "v", "?column?"}, Types: []string{"TEXT", "TEXT", "INT4"}, Rows: [][]any{{"2", "2", int64(2)}}, Bag: false})
	}
}
