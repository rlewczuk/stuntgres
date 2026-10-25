package enginetest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// RoleOperation starts a session authenticated as the login role name and
// acquires its operation. The cleanup releases the operation and ends the
// session.
func RoleOperation(t testing.TB, e *core.Engine, name string) (*session.Operation, func()) {
	t.Helper()
	ref, err := e.ResolveLogin(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(sessionID.Add(1)), Authenticated: ref})
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

// RoleError requires that sql fails on op with exactly the diagnostic code,
// message and detail, at no position.
func RoleError(t testing.TB, e *core.Engine, op *session.Operation, sql, code, message, detail string) {
	t.Helper()
	RoleErrorPosition(t, e, op, sql, code, message, detail, "")
}

// RoleErrorPosition is RoleError with an expected position.
func RoleErrorPosition(t testing.TB, e *core.Engine, op *session.Operation, sql, code, message, detail, position string) {
	t.Helper()
	_, err := e.Execute(op, Statement(t, sql), 0, nil)
	var d *sqlstate.Error
	if !errors.As(err, &d) {
		t.Fatalf("%s: %T %v", sql, err, err)
	}
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail, Position: position}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("%s: %#v want %#v", sql, d, want)
	}
}

// RoleExecute executes sql without parameters on op, fails the test on error
// and closes the result rows at test cleanup.
func RoleExecute(t testing.TB, e *core.Engine, op *session.Operation, sql string) core.Result {
	t.Helper()
	r, err := e.Execute(op, Statement(t, sql), 0, nil)
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
