package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestMergeDeleteDeletionBaseAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	original := e.Storage.Relations[ref.Address].Versions[0]
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "UPDATE t SET v=11")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(enginetest.Statement(t, "MERGE INTO t USING (VALUES(1)) src(id) ON t.id=src.id WHEN MATCHED THEN DELETE"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 1 {
		scope.Finish(err)
		t.Fatal("own replacement deletion count", result.Count)
	}
	root := scope.Stage.Deletes[ref.Address]
	candidate := root.Rows[original.Row]
	if !candidate.Own || candidate.Base != original.Version || candidate.Version == original.Version {
		scope.Finish(err)
		t.Fatal("deletion lost derived replacement base", candidate, original)
	}
	candidate.Base++
	root.Rows[original.Row] = candidate
	if err = scope.Finish(nil); err != nil {
		t.Fatal("statement stage Finish unexpectedly publishes", err)
	}
	op.Done()
	commit := enginetest.TransactionOperation(t, s)
	_, err = e.Commit(commit, tx)
	commit.Done()
	if err == nil {
		t.Fatal("publication accepted forged replacement base")
	}
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
}
