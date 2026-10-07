package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestBetweenProjectionEndpoints(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "between-projection-endpoints")
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v INTEGER)")
	execSQL(t, db, "INSERT INTO t VALUES(1,0),(2,1),(3,2),(4,3),(5,4),(6,NULL)")
	const query = "SELECT v BETWEEN 1 AND 3,v NOT BETWEEN 1 AND 3,v BETWEEN SYMMETRIC 3 AND 1,v NOT BETWEEN SYMMETRIC 3 AND 1 FROM t ORDER BY id"
	compareOpsDirectPrepared(t, db, query, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
		{false, true, false, true},
		{true, false, true, false},
		{true, false, true, false},
		{true, false, true, false},
		{false, true, false, true},
		{nil, nil, nil, nil},
	})
}
