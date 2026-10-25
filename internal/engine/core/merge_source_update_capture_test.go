package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestMergeSourceUpdateCapture(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "INSERT INTO t VALUES(1,10),(2,20),(2,20)", "INSERT INTO source VALUES(1),(1)"} {
		sessionSQL(t, peer, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100")
	defer op.Done()
	if target.Count() != 3 {
		scope.Finish(context.Canceled)
		t.Fatal(target.Count())
	}
	first := target.VersionAt(target.RawIndex(1))
	second := target.VersionAt(target.RawIndex(2))
	if first.Row == second.Row {
		scope.Finish(context.Canceled)
		t.Fatal("distinct equal targets lost identity")
	}
	sessionSQL(t, peer, "UPDATE source SET id=2")
	sessionSQL(t, peer, "INSERT INTO t VALUES(3,30)")
	result, err := core.ExecuteMerge(context.Background(), scope, b, target, inputs, nil, "public")
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 2 {
		scope.Finish(context.Canceled)
		t.Fatal(result)
	}
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 20, 30)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 120, 120, 30)
	enginetest.LockEmpty(t, e)
}
