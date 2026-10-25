package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// Follow the existing queryJoinsFixture capture seam, but on the actual hash
// owner's engine. No public adapter or testing-only production switch is used.
func hashCaptureQuery(t *testing.T, e *core.Engine, op *session.Operation, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := statement(t, q)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var bound analyze.Binding
	if err == nil {
		bound, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	var source *exec.Source
	if err == nil {
		// Logical-stage fixtures supply cancellation contexts directly, but keep
		// the actual admission alive until every retained stage is closed.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, bound.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		source.Close()
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	return bound.Query, source, release
}

func hashImplicitQuery(t *testing.T, e *core.Engine, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	query, source, release := hashCaptureQuery(t, e, op, q)
	return query, source, func() {
		release()
		done()
	}
}

func hashFilterStage(t *testing.T, query *plan.Query, source *exec.Source, parameters []sqltype.Value) exec.Stage {
	t.Helper()
	filter := exec.HashQueryFilter(query.Root)
	if filter == nil {
		t.Fatal("fixture has no top-level filter")
	}
	state := &exec.State{Source: source, Parameters: parameters, HashFilter: filter, Frame: query.Root.(*plan.ProjectNode).Frame}
	if scope := core.ScopeOf(source.LiveHost()); scope != nil {
		state.Input, state.Output = scope.Input, scope.Output
	}
	stage, err := exec.OpenNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	return stage
}
