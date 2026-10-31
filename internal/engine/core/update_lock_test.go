package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestUpdateCoreImmediateGrantRecheck(t *testing.T) {
	for _, predicateChanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh_rhs", true: "fresh_predicate"}[predicateChanged], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(id INT,a INT,b INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1,10,0),(2,20,0)")
			op, scope, bound, source := updateBoundSource(t, e, b, context.Background(), "UPDATE t SET b=a WHERE id=1")
			original := source.VersionAt(source.RawIndex(0))
			if predicateChanged {
				enginetest.SessionSQL(t, a, "UPDATE t SET id=3,a=11 WHERE id=1")
				enginetest.SessionSQL(t, a, "UPDATE t SET id=1 WHERE id=2")
			} else {
				enginetest.SessionSQL(t, a, "UPDATE t SET a=11 WHERE id=1")
			}
			// The peer has committed and released the row before acquisition,
			// but this complete executor still starts from its captured old view.
			r, err := core.ExecuteUpdate(op.Ctx, scope, bound, source, nil, nil, "public")
			want := int64(1)
			if predicateChanged {
				want = 0
			}
			if err != nil || r.Count != want {
				scope.Finish(err)
				op.Done()
				t.Fatal(r, err)
			}
			if err = scope.Finish(nil); err != nil {
				op.Done()
				t.Fatal(err)
			}
			op.Done()
			if enginetest.Export(original.Values[1]) != int64(10) {
				t.Fatal("snapshot rewritten")
			}
			if predicateChanged {
				enginetest.TransactionRows(t, a, "SELECT b FROM t ORDER BY a", 0, 0)
			} else {
				enginetest.TransactionRows(t, a, "SELECT b FROM t ORDER BY a", 11, 0)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
