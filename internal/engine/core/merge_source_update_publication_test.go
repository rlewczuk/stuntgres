package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

// The pinned PG18 correlated scalar array-bound witness crashes the server.
// This is an actual native executor safety proof, not PostgreSQL parity.
func TestMergeSourceUpdateCorrelatedArrayBound(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,a INT[])")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(2,ARRAY[10,20,30])")
	op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a[(SELECT t.id)]=t.a[1],a[1]=t.a[t.id]")
	defer op.Done()
	result, err := core.ExecuteMerge(context.Background(), scope, b, target, inputs, nil, "public")
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 1 {
		scope.Finish(context.Canceled)
		t.Fatal(result)
	}
	enginetest.TransactionRows(t, peer, "SELECT a[1] FROM t", 10)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionRows(t, peer, "SELECT a[1] FROM t", 20)
	enginetest.TransactionRows(t, peer, "SELECT a[2] FROM t", 10)
	enginetest.TransactionRows(t, peer, "SELECT a[3] FROM t", 30)
	enginetest.LockEmpty(t, e)
}
