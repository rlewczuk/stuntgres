package enginetest

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

// OpenSession exercises public admission before startup and returns failures to
// lifecycle tests. An omitted principal uses the engine's trusted postgres login.
func OpenSession(ctx context.Context, e *core.Engine, start session.SessionStart, principals ...authorization.PrincipalRef) (*session.Session, error) {
	var principal authorization.PrincipalRef
	if len(principals) != 0 {
		principal = principals[0]
	} else {
		var err error
		principal, err = e.ResolveLogin("postgres")
		if err != nil {
			return nil, err
		}
	}
	admission, err := e.AdmitDatabase(ctx, principal, core.DatabaseSelector{Name: e.DatabaseName, Explicit: true})
	if err != nil {
		return nil, err
	}
	defer admission.Release()
	return e.StartSession(ctx, admission, start)
}

// StartSession starts a session with session defaults from p (nil for none)
// and ends it at test cleanup.
func StartSession(t testing.TB, e *core.Engine, p settings.SessionDefaultProvider) *session.Session {
	t.Helper()
	s, err := OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(sessionID.Add(1)), Defaults: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

// SessionSQL executes q without parameters in one operation of s, fails the
// test on error and closes the result rows at test cleanup.
func SessionSQL(t testing.TB, s *session.Session, q string) core.Result {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, Statement(t, q), 0, nil)
	if err != nil {
		t.Fatal(err)
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

// TransactionOperation acquires an operation of s and releases it at test
// cleanup.
func TransactionOperation(t testing.TB, s *session.Session) *session.Operation {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}

// TransactionBegin begins an explicit transaction on s.
func TransactionBegin(t testing.TB, s *session.Session) ids.TxRef {
	t.Helper()
	op := TransactionOperation(t, s)
	defer op.Done()
	ref, err := core.EngineOf(s).Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// TransactionEnd commits or rolls back the transaction ref of s.
func TransactionEnd(t testing.TB, s *session.Session, ref ids.TxRef, commit bool) core.EndResult {
	t.Helper()
	op := TransactionOperation(t, s)
	defer op.Done()
	var r core.EndResult
	var err error
	if commit {
		r, err = core.EngineOf(s).Commit(op, ref)
	} else {
		r, err = core.EngineOf(s).Rollback(op, ref)
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TransactionRows runs q on s and requires one integer column with the rows
// want, in order.
func TransactionRows(t testing.TB, s *session.Session, q string, want ...int64) *exec.FinalRows {
	t.Helper()
	r := SessionSQL(t, s, q)
	t.Cleanup(func() {
		if err := r.Rows.Close(); err != nil {
			t.Error(err)
		}
	})
	got := Tuples(r)
	expected := make([][]driver.Value, len(want))
	for i, v := range want {
		expected[i] = []driver.Value{v}
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("%s got %v want %v", q, got, expected)
	}
	return r.Rows.(*exec.FinalRows)
}
