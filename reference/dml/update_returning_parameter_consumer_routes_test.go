package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningFreshHeldParametersConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,a INT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,10),(2,20)")
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
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"a", "a", "label"}, Types: []string{"INT4", "INT4", "TEXT"}, Rows: tc.want, Bag: false})
		}
	}
}
