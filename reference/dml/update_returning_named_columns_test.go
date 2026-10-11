package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningNamedColumns(t *testing.T) {
	for _, name := range []string{"old", "new"} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			db := backend.DB
			fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE "+name+"(a INT,old INT,new INT)")
			fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO "+name+" VALUES(10,20,30)")
			rows, err := db.Query("UPDATE " + name + " SET a=a+1 RETURNING WITH (OLD AS o) " + name + ".a,o.a,old,new")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"a", "a", "old", "new"}, Types: []string{"INT4", "INT4", "INT4", "INT4"}, Rows: [][]any{{int64(11), int64(10), int64(20), int64(30)}}, Bag: false})

		}
	}
}
