package engine

import (
	"context"
	"testing"
)

func TestMergeSourceUpdatePublication(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	before := append([]rowVersion(nil), e.storage.Relations[ref.Address].Versions...)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 2 || len(scope.stage.rows) == 0 {
		scope.Finish(context.Canceled)
		t.Fatal(result, scope.stage.rows)
	}
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 110, 120)
	lockEmpty(t, e)
	for i, old := range before {
		value, err := old.Values[1].Integer()
		if err != nil || value != int64((i+1)*10) {
			t.Fatal("retained row changed", value, err)
		}
	}
}

// The pinned PG18 correlated scalar array-bound witness crashes the server.
// This is an actual native executor safety proof, not PostgreSQL parity.
func TestMergeSourceUpdateCorrelatedArrayBound(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT,a INT[])")
	sessionSQL(t, peer, "INSERT INTO t VALUES(2,ARRAY[10,20,30])")
	op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a[(SELECT t.id)]=t.a[1],a[1]=t.a[t.id]")
	defer op.Done()
	result, err := e.executeMerge(context.Background(), scope, b, target, inputs, nil, "public")
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 1 {
		scope.Finish(context.Canceled)
		t.Fatal(result)
	}
	transactionRows(t, peer, "SELECT a[1] FROM t", 10)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	transactionRows(t, peer, "SELECT a[1] FROM t", 20)
	transactionRows(t, peer, "SELECT a[2] FROM t", 10)
	transactionRows(t, peer, "SELECT a[3] FROM t", 30)
	lockEmpty(t, e)
}
