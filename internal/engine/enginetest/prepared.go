package enginetest

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// PreparedDescription parses query and describes it in one operation of s,
// failing the test on error.
func PreparedDescription(t testing.TB, e *core.Engine, s *session.Session, query string) (parser.Statement, core.Description) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := Statement(t, query)
	d, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	return ast, d
}

// PreparedExecute executes ast in one statement of s against the expected
// description d, as a prepared execution does, and returns the finished
// statement's result.
func PreparedExecute(e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description) (core.Result, error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return core.Result{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return core.Result{}, err
	}
	scope.Expected = d
	r, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	return r, err
}
