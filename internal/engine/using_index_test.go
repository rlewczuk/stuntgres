package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Package-entry fixtures use independently built ASTs, not driver SQL. Catalog,
// providers, committed rows, baseline roots and transaction owners are real.
func usingIndexUnitExecute(t *testing.T, e *core.Engine, s *session.Session, statement parser.Statement) core.Result {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := e.Execute(op, statement, 0, nil)
	if err != nil || result.Rows != nil {
		t.Fatal(result, err)
	}
	return result
}
