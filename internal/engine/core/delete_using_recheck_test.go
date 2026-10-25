package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestDeleteUsingLateRecheckFailureDiscardsStage(t *testing.T) {
	e := engine.New()
	writer := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1),(2)"} {
		enginetest.SessionSQL(t, peer, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, writer, context.Background(), "DELETE FROM t USING s WHERE t.id=s.k RETURNING t.id")
	enginetest.SessionSQL(t, peer, "UPDATE t SET v=21 WHERE id=2")
	sentinel := errors.New("latest-target recheck failure")
	predicate := b.Predicate
	failed := false
	b.Predicate = queryProbe(predicate.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		if enginetest.Export(c.Row[1]) == int64(21) {
			root := scope.Stage.Deletes[b.Ref.Address]
			if root == nil || len(root.Rows) != 1 {
				t.Fatal("recheck did not follow a staged earlier target", root)
			}
			failed = true
			return sqltype.Value{}, sentinel
		}
		return predicate.Eval(c)
	})
	r, err := dml.ExecuteDelete(scope.WriteHost(), dml.DeleteInput{Binding: b, Source: target, Inputs: inputs})
	if r.Rows != nil {
		t.Cleanup(func() { r.Rows.Close() })
	}
	target.Close()
	if !failed || !errors.Is(err, sentinel) || r.Count != 0 || r.Rows != nil {
		t.Fatal(r, err, failed)
	}
	if err := scope.Finish(err); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	op.Done()
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 21)
	enginetest.LockEmpty(t, e)
}

func TestDeleteUsingSourceGrantsStayReadOnly(t *testing.T) {
	e := engine.New()
	writer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1)", "INSERT INTO s VALUES(1)"} {
		enginetest.SessionSQL(t, writer, q)
	}
	tx := enginetest.TransactionBegin(t, writer)
	deleteCount(t, writer, "DELETE FROM t USING s WHERE t.id=s.k", 1)
	target := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	source := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
	lockState := e.Locks.Inspect()
	var targetMode, sourceMode lock.Mode
	for resource, entry := range lockState.Resources {
		if resource.ObjectRef == target && resource.Row == 0 {
			targetMode = entry.Holders[writer.Transactions.Ticket].Mode
		}
		if resource.ObjectRef == source {
			if resource.Row != 0 {
				t.Fatal("ordinary source tuple write grant", resource)
			}
			sourceMode = entry.Holders[writer.Transactions.Ticket].Mode
		}
	}
	if targetMode&lock.RowExclusive == 0 || sourceMode&lock.AccessShare == 0 || sourceMode&lock.RowExclusive != 0 {
		t.Fatal(targetMode, sourceMode)
	}
	enginetest.TransactionEnd(t, writer, tx, false)
	enginetest.TransactionRows(t, writer, "SELECT id FROM t", 1)
	enginetest.LockEmpty(t, e)
}

// This fixture exercises package admission/binding/capture functions, not the
// public driver. Captures use exactly the executor's combined relation set.
func updateFromBoundSources(t *testing.T, e *engine.Engine, session *session.Session, ctx context.Context, q string) (*session.Operation, *engine.StatementScope, analyze.Binding, *exec.Source, *exec.Source) {
	t.Helper()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var scope *engine.StatementScope
	var source, from *exec.Source
	// Register ownership before parsing, admission or capture can fail. Normal
	// callers still release immediately; these idempotent fallbacks also cover
	// Fatal/panic exits before a caller reaches Finish/Done.
	t.Cleanup(func() {
		if from != nil {
			from.Close()
		}
		if source != nil {
			source.Close()
		}
		if scope != nil && !scope.Finished {
			scope.Finish(errors.New("abandoned mutation capture fixture"))
		}
		op.Done()
	})
	scope, err = e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	fail := func(err error) { scope.Finish(err); op.Done(); t.Fatal(err) }
	ast := enginetest.Statement(t, q)
	if err = scope.Gate(ast); err != nil {
		fail(err)
	}
	if err = scope.AcquireRelation(ast, true); err != nil {
		fail(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	var view *visibility.View
	if err == nil {
		b, err = engine.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
		scope.RoutineAuth, scope.RoutineDependencies = auth, b.Dependencies
	}
	if err == nil {
		err = e.InitializeQueryLocksLocked(scope, nil, b.QuerySources)
	}
	if err == nil {
		err = e.CaptureCommentSnapshotLocked(scope)
	}
	var refs []ids.ObjectRef
	if err == nil {
		refs, err = e.QueryTableRefsLocked(auth, auth.State.CurrentUser, b.QuerySources, []ids.ObjectRef{b.Ref}, scope.Op.Session.SessionToken, plan.RowMark{})
	}
	if err == nil {
		_, err = scope.AcquireDataViewsLocked(e, refs)
	}
	if err == nil {
		view, err = scope.AcquireDataViewLocked(e, b.Ref)
	}
	if err == nil {
		source, err = exec.CaptureSource(op.Ctx, view, b.Ref)
	}
	if err == nil {
		from, err = e.CaptureQuerySourcesLocked(scope, b.QuerySources, false)
	}
	e.Mu.Unlock()
	if err != nil {
		if source != nil {
			source.Close()
		}
		fail(err)
	}
	return op, scope, b, source, from
}
