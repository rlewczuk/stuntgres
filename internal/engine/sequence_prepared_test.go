package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func sequencePreparedDescription(t *testing.T, e *core.Engine, s *session.Session, query string) (parser.Statement, core.StatementDescription) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := statement(t, query)
	d, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	return ast, d
}
