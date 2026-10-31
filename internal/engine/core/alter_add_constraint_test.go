package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func alterConstraintExecuteScope(t *testing.T, e *core.Engine, s *session.Session, q string) (*core.StatementScope, error) {
	t.Helper()
	op := alterConstraintOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
	})
	raw, err := parser.Parse(q)
	if err == nil {
		_, err = scope.Execute(raw[0], 0, nil)
	}
	return scope, err
}

func alterConstraintOperation(t *testing.T, s *session.Session) *session.Operation {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}
