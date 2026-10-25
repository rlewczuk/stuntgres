package enginetest

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// RoleOperation starts a session authenticated as the login role name and
// acquires its operation. The cleanup releases the operation and ends the
// session.
func RoleOperation(t testing.TB, e *engine.Engine, name string) (*session.Operation, func()) {
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

// RoleExecute executes sql without parameters on op, fails the test on error
// and closes the result rows at test cleanup.
func RoleExecute(t testing.TB, e *engine.Engine, op *session.Operation, sql string) engine.Result {
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
