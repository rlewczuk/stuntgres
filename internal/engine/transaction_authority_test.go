package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTransactionAttachmentIncarnationExcludesReusedSessionID(t *testing.T) {
	e := New()
	start := func() *session.Session {
		s, err := e.StartSession(context.Background(), session.SessionStart{ID: 1000000})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.RequestEnd(session.CleanClose); _ = s.WaitEnd(context.Background()) })
		return s
	}
	old := start()
	oldRef := transactionBegin(t, old)
	transactionEnd(t, old, oldRef, false)
	old.RequestEnd(session.CleanClose)
	if err := old.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	current := start()
	ref := transactionBegin(t, current)
	if oldRef == ref {
		t.Fatal("physical attachment incarnation reused")
	}
	op := transactionOperation(t, current)
	defer op.Done()
	if _, err := e.Commit(op, oldRef); err == nil {
		t.Fatal("old attachment ended replacement work")
	}
	if current.TransactionState() != session.TxActive {
		t.Fatal("foreign authority changed state")
	}
	transactionEnd(t, current, ref, false)
}

func TestTransactionCopiedScopeStageViewAndSnapshot(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(7)")
	ref := transactionBegin(t, s)
	op := transactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	copyScope := *scope
	if err = copyScope.Finish(nil); err == nil {
		t.Fatal("copied scope released owner serialization")
	}
	result, err := scope.Execute(statement(t, "SELECT v FROM t"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := scope.view.Data()
	var relation ids.ObjectRef
	for a := range data.Committed {
		relation = e.catalog.Domain.Ref(data.Catalog.Epoch(), a)
	}
	stage := *scope.Writes()
	before := e.storage.RowFrontier()
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
	if e.storage.RowFrontier() != before {
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
	transactionEnd(t, s, ref, false)
}

func TestTransactionRollbackAndAbortDoNotPublishFrontier(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	sessionSQL(t, b, "INSERT INTO t VALUES(7)")
	frontier := e.visibility.CommitSeq()
	for _, abort := range []bool{false, true} {
		ref := transactionBegin(t, a)
		sessionSQL(t, a, "INSERT INTO t VALUES(1)")
		if abort {
			op := transactionOperation(t, a)
			_, err := e.Execute(op, statement(t, "INSERT INTO t VALUES(2),(NULL)"), 0, nil)
			state(t, err, "23502")
			op.Done()
			if transactionEnd(t, a, ref, true).Committed {
				t.Fatal("aborted end reported commit")
			}
		} else {
			transactionEnd(t, a, ref, false)
		}
		if e.visibility.CommitSeq() != frontier {
			t.Fatal("discard emitted a committed frontier")
		}
		transactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
	}
	ref := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(3)")
	if !transactionEnd(t, a, ref, true).Committed || e.visibility.CommitSeq() != frontier+1 {
		t.Fatal("successful commit did not advance exactly one frontier")
	}
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 3, 7)
}
