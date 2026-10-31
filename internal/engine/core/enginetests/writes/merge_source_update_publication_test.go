package writes_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
)

func TestMergeSourceUpdatePublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	before := append([]storage.RowVersion(nil), e.Storage.Relations[ref.Address].Versions...)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(enginetest.Statement(t, "MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 2 || len(scope.Stage.Rows) == 0 {
		scope.Finish(context.Canceled)
		t.Fatal(result, scope.Stage.Rows)
	}
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 110, 120)
	enginetest.LockEmpty(t, e)
	for i, old := range before {
		value, err := old.Values[1].Integer()
		if err != nil || value != int64((i+1)*10) {
			t.Fatal("retained row changed", value, err)
		}
	}
}
