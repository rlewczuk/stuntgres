package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func amcheckSessionCall(t *testing.T, e *core.Engine, s *session.Session, query string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	return err
}
