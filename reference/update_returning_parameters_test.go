package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningFreshHeldParameters(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		r := backend.recorder
		referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT,a INT)", 0)
		referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1,10),(2,20)", 2)
		q := "UPDATE t SET a=$1 WHERE id=$2 RETURNING old.a,new.a,$3 AS label"
		held, err := db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		executor := referenceHeldStatement{referenceExecutor: db, statement: held, query: q}
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
			referenceRunStep(t, p.ctx, executor, r, referenceSelect(q, []string{"a", "a", "label"}, []string{"INT4", "INT4", "TEXT"}, tc.want, tc.a, tc.id, tc.label))
		}
		held.Close()
	}
}
