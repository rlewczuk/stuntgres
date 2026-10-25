package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Utility Prepare is analysis-only; both routes execute through a real scope.
func sequenceCreateRestartCommand(t *testing.T, e *core.Engine, s *session.Session, query string, prepared bool) ([]sqlstate.Error, error) {
	t.Helper()
	ast := statement(t, query)
	var expected *core.StatementDescription
	if prepared {
		var description core.StatementDescription
		ast, description = sequencePreparedDescription(t, e, s, query)
		expected = &description
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = expected
	r, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	if r.Rows != nil {
		if closeErr := r.Rows.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("sequence utility returned rows")
	}
	if r.Count != 0 {
		t.Fatal("sequence utility affected rows", r.Count)
	}
	return op.DetachNotices(), err
}

func sequenceCreateRestartDiagnostic(t *testing.T, err error, code, message, position string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
		t.Fatalf("diagnostic=%#v error=%v want=%#v", got, err, want)
	}
}
