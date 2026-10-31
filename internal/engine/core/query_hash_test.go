package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func hashImplicitQuery(t *testing.T, e *core.Engine, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	query, source, release := enginetest.HashCaptureQuery(t, e, op, q)
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
	exec.AdmitScan(query.Root, source)
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
