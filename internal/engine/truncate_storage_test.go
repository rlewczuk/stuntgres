package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func truncateUnitAST(names ...string) *parser.Truncate {
	stmt := &parser.Truncate{}
	for _, name := range names {
		stmt.Targets = append(stmt.Targets, parser.LockTarget{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}})
	}
	return stmt
}

func truncateUnitFixture(t *testing.T) (*Engine, *session.Session, ids.ObjectRef, ids.ObjectRef) {
	t.Helper()
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE q(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1),(2)", "INSERT INTO q VALUES(7)"} {
		sessionSQL(t, s, q)
	}
	return e, s, roleTableRef(t, e, "p"), roleTableRef(t, e, "q")
}

func truncateUnitScope(t *testing.T, e *Engine, s *session.Session, ctx context.Context) (*StatementScope, *session.Operation) {
	t.Helper()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(errors.New("truncate unit cleanup")); op.Done() })
	return scope, op
}

func truncateUnitStage(t *testing.T, e *Engine, s *session.Session, names ...string) *StatementScope {
	t.Helper()
	scope, _ := truncateUnitScope(t, e, s, context.Background())
	if _, err := scope.Execute(truncateUnitAST(names...), 0, nil); err != nil {
		t.Fatal(err)
	}
	return scope
}

func truncateUnitExecute(t *testing.T, e *Engine, s *session.Session, stmt *parser.Truncate) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	if _, err = e.Execute(op, stmt, 0, nil); err != nil {
		t.Fatal(err)
	}
}
