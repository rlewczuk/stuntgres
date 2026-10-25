package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
)

func conflictUpdateAnalyzedScope(t *testing.T, e *core.Engine, s *session.Session, q string) (*session.Operation, *core.StatementScope, analyze.Binding) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(errors.New("unit rollback"))
		}
		op.Done()
	})
	stmt := statement(t, q)
	if err := scope.Gate(stmt); err != nil {
		t.Fatal(err)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	if err == nil {
		b, err = core.Describe(op.Context(), e.AnalysisHost(op), stmt, auth, ref, scope.Input, nil, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: scope.Input, Output: scope.Output, Host: core.HostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	conflict := dml.NewInsertConflictState(b.Insert.Conflict)
	if err := conflict.SelectArbiters(&evaluation, b.Relation, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if err := dml.PreflightConflictUpdate(conflict, &evaluation); err != nil {
		t.Fatal(err)
	}
	scope.InsertConflict = conflict
	return op, scope, b
}
