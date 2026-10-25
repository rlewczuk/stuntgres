package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func sessionStart(t *testing.T, e *core.Engine, p settings.SessionDefaultProvider) *session.Session {
	t.Helper()
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: p})
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

func sessionSQL(t *testing.T, s *session.Session, q string) core.Result {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, statement(t, q), 0, nil)
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
