package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestTransactionAttachmentIncarnationExcludesReusedSessionID(t *testing.T) {
	e := engine.New()
	start := func() *session.Session {
		s, err := e.StartSession(context.Background(), session.SessionStart{ID: 1000000})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.RequestEnd(session.CleanClose); _ = s.WaitEnd(context.Background()) })
		return s
	}
	old := start()
	oldRef := enginetest.TransactionBegin(t, old)
	enginetest.TransactionEnd(t, old, oldRef, false)
	old.RequestEnd(session.CleanClose)
	if err := old.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	current := start()
	ref := enginetest.TransactionBegin(t, current)
	if oldRef == ref {
		t.Fatal("physical attachment incarnation reused")
	}
	op := enginetest.TransactionOperation(t, current)
	defer op.Done()
	if _, err := e.Commit(op, oldRef); err == nil {
		t.Fatal("old attachment ended replacement work")
	}
	if current.TransactionState() != session.TxActive {
		t.Fatal("foreign authority changed state")
	}
	enginetest.TransactionEnd(t, current, ref, false)
}

func TestTransactionCopiedScopeStageViewAndSnapshot(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(7)")
	ref := enginetest.TransactionBegin(t, s)
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	copyScope := *scope
	if err = copyScope.Finish(nil); err == nil {
		t.Fatal("copied scope released owner serialization")
	}
	result, err := scope.Execute(enginetest.Statement(t, "SELECT v FROM t"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := scope.View.Data()
	var relation ids.ObjectRef
	for a := range data.Committed {
		relation = e.Catalog.Domain.Ref(data.Catalog.Epoch(), a)
	}
	stage := *scope.Writes()
	before := e.Storage.RowFrontier()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	value, err := sqltype.Integer(typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Apply(relation, [][]sqltype.Value{{value}}); err == nil {
		t.Fatal("copied stage accepted writes")
	}
	if err = scope.Writes().Apply(ids.ObjectRef{}, [][]sqltype.Value{{value}}); err == nil {
		t.Fatal("foreign relation staged")
	}
	if err = scope.Writes().Apply(relation, [][]sqltype.Value{{}}); err == nil {
		t.Fatal("invalid payload staged")
	}
	if e.Storage.RowFrontier() != before {
		t.Fatal("failed Apply transferred ownership/allocated identity")
	}
	actual := result.Rows.(*exec.FinalRows)
	forgedResult := &exec.FinalRows{Self: actual, Source: actual.Source}
	forgedResult.Close()
	copiedResult := *actual
	copiedResult.Close()
	if actual.Source.View.Catalog().Epoch() == 0 {
		t.Fatal("copied result released another result")
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	result.Rows.Close()
	enginetest.TransactionEnd(t, s, ref, false)
}

func TestTransactionRollbackAndAbortDoNotPublishFrontier(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
	frontier := e.Visibility.CommitSeq()
	for _, abort := range []bool{false, true} {
		ref := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
		if abort {
			op := enginetest.TransactionOperation(t, a)
			_, err := e.Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(2),(NULL)"), 0, nil)
			enginetest.State(t, err, "23502")
			op.Done()
			if enginetest.TransactionEnd(t, a, ref, true).Committed {
				t.Fatal("aborted end reported commit")
			}
		} else {
			enginetest.TransactionEnd(t, a, ref, false)
		}
		if e.Visibility.CommitSeq() != frontier {
			t.Fatal("discard emitted a committed frontier")
		}
		enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
	}
	ref := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(3)")
	if !enginetest.TransactionEnd(t, a, ref, true).Committed || e.Visibility.CommitSeq() != frontier+1 {
		t.Fatal("successful commit did not advance exactly one frontier")
	}
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 3, 7)
}
