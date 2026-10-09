package reference_test

import "testing"

func TestReferenceUpdateReturningNamedColumns(t *testing.T) {
	for _, name := range []string{"old", "new"} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE "+name+"(a INT,old INT,new INT)")
			updateReturningReferenceExec(t, db, "INSERT INTO "+name+" VALUES(10,20,30)")
			rows, err := db.Query("UPDATE " + name + " SET a=a+1 RETURNING WITH (OLD AS o) " + name + ".a,o.a,old,new")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"a", "a", "old", "new"}, types: []string{"INT4", "INT4", "INT4", "INT4"}, rows: [][]any{{int64(11), int64(10), int64(20), int64(30)}}, bag: false})

		}
	}
}
