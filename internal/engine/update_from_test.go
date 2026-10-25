package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
)

// This fixture exercises package admission/binding/capture functions, not the
// public driver. Captures use exactly the executor's combined relation set.
func updateFromBoundSources(t *testing.T, e *core.Engine, session *session.Session, ctx context.Context, q string) (*session.Operation, *core.StatementScope, analyze.Binding, *exec.Source, *exec.Source) {
	t.Helper()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var scope *core.StatementScope
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
	ast := statement(t, q)
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
		b, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
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
