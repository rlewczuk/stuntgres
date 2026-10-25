package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func roleExecute(t *testing.T, e *core.Engine, op *session.Operation, sql string) core.Result {
	t.Helper()
	r, err := e.Execute(op, statement(t, sql), 0, nil)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if r.Rows != nil {
		t.Cleanup(func() {
			if err := r.Rows.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}

func roleError(t *testing.T, e *core.Engine, op *session.Operation, sql, code, message, detail string) {
	t.Helper()
	roleErrorPosition(t, e, op, sql, code, message, detail, "")
}

func roleErrorPosition(t *testing.T, e *core.Engine, op *session.Operation, sql, code, message, detail, position string) {
	t.Helper()
	_, err := e.Execute(op, statement(t, sql), 0, nil)
	var d *sqlstate.Error
	if !errors.As(err, &d) {
		t.Fatalf("%s: %T %v", sql, err, err)
	}
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail, Position: position}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("%s: %#v want %#v", sql, d, want)
	}
}

func roleOperation(t *testing.T, e *core.Engine, name string) (*session.Operation, func()) {
	t.Helper()
	ref, err := e.ResolveLogin(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: ref})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return op, func() {
		op.Done()
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}
}

func roleIdentity(t *testing.T, e *core.Engine, op *session.Operation, want string) {
	t.Helper()
	r := roleExecute(t, e, op, "SELECT session_user,current_user")
	values := tuples(r)
	if r.Count != 1 || !reflect.DeepEqual(values, [][]driver.Value{{want, want}}) || r.Rows.Schema()[0].Usage.Type.ID() != sqltype.Name || r.Rows.Schema()[1].Usage.Type.ID() != sqltype.Name {
		t.Fatalf("identity: %#v %#v", values, r.Rows.Schema())
	}
}
