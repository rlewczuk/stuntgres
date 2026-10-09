package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningFreshHeldParametersConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,a INT)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,10),(2,20)")
		held, err := db.Prepare("UPDATE t SET a=$1 WHERE id=$2 RETURNING old.a,new.a,$3 AS label")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error("statement close", err)
			}
		}()
		for _, tc := range []struct {
			a, id any
			label any
			want  [][]any
		}{
			{11, 1, "first", [][]any{{int64(10), int64(11), "first"}}},
			{nil, 2, nil, [][]any{{int64(20), nil, nil}}},
			{12, 1, "third", [][]any{{int64(11), int64(12), "third"}}},
			{99, 99, "empty", [][]any{}},
		} {
			rows, err := held.Query(tc.a, tc.id, tc.label)
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"a", "a", "label"}, types: []string{"INT4", "INT4", "TEXT"}, rows: tc.want, bag: false})
		}
	}
}
