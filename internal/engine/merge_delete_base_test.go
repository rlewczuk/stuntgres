package engine

import (
	"context"
	"testing"
)

func TestMergeDeleteDeletionBaseAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	original := e.storage[ref.Address].versions[0]
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE t SET v=11")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "MERGE INTO t USING (VALUES(1)) src(id) ON t.id=src.id WHEN MATCHED THEN DELETE"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 1 {
		scope.Finish(err)
		t.Fatal("own replacement deletion count", result.Count)
	}
	root := scope.stage.deletes[ref.Address]
	candidate := root.rows[original.row]
	if !candidate.own || candidate.base != original.version || candidate.version == original.version {
		scope.Finish(err)
		t.Fatal("deletion lost derived replacement base", candidate, original)
	}
	candidate.base++
	root.rows[original.row] = candidate
	if err = scope.Finish(nil); err != nil {
		t.Fatal("statement stage Finish unexpectedly publishes", err)
	}
	op.Done()
	commit := transactionOperation(t, s)
	_, err = e.Commit(commit, tx)
	commit.Done()
	if err == nil {
		t.Fatal("publication accepted forged replacement base")
	}
	transactionRows(t, peer, "SELECT v FROM t", 10)
}
