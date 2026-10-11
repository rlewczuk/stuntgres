package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningFreshHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		r := backend.Recorder
		fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT,a INT)", 0)
		fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1,10),(2,20)", 2)
		q := "UPDATE t SET a=$1 WHERE id=$2 RETURNING old.a,new.a,$3 AS label"
		held, err := db.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		executor := fixture.HeldStatement{Executor: db, Statement: held, SQL: q}
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
			fixture.RunStep(t, p.Ctx, executor, r, fixture.Select(q, []string{"a", "a", "label"}, []string{"INT4", "INT4", "TEXT"}, tc.want, tc.a, tc.id, tc.label))
		}
		held.Close()
	}
}
