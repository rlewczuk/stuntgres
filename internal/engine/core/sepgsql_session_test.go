package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func sepgsqlSessionCleaned(t *testing.T, s *session.Session, handle session.ResourceHandle, generation uint64) {
	t.Helper()
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.State != session.Ended || s.Leases != 0 || len(s.Operations) != 0 || !s.Removed[handle] {
		t.Fatal("terminal session did not consume its subject resource")
	}
	for kind, entry := range s.Resources {
		if entry != nil {
			t.Errorf("terminal resource %d retained", kind)
		}
	}
	if len(s.Removed) != int(s.Serial) {
		t.Errorf("removed resources=%d attached=%d", len(s.Removed), s.Serial)
	}
	subject := s.Sepgsql
	current, gotGeneration, stamp, depth := subject.PhaseSnapshot()
	if current != "" || subject.Original() != "" || subject.Mark() != (session.SepgsqlMark{}) || stamp != 0 || depth != 0 || gotGeneration != generation {
		t.Fatalf("subject retained terminal state: current=%q original=%q generation=%d want=%d stamp=%d depth=%d", current, subject.Original(), gotGeneration, generation, stamp, depth)
	}
}

func TestSepgsqlSimulationSessionCleanup(t *testing.T) {
	e := sepgsqlPhaseEngine(t)
	s, err := enginetest.OpenSession(t.Context(), e, session.SessionStart{ID: enginetest.NextSessionID()})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("subject consumer cleanup failed")
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.WaitEnd(ctx); err != nil && !errors.Is(err, failure) {
			t.Error(err)
		}
	})
	subject := s.Sepgsql
	entry := s.Resources[session.SepgsqlSecurity]
	if subject == nil || entry == nil || entry.Owner != subject || subject.Original() != "A" {
		t.Fatal("startup did not attach the actual physical subject")
	}
	handle := entry.Handle
	enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('A')")
	trusted := subject.Enter("T")
	// The registered subject must outlive both the operation and terminal
	// consumers, including transaction rollback of the pending context.
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	calls := 0
	var beforeCleanup uint64
	if _, err = op.Attach(session.Temporary, &sessionOwner{fn: func(token ids.SessionToken, reason session.EndReason) error {
		calls++
		if !s.Mu.TryLock() {
			t.Error("resource callback ran under Session.Mu")
		} else {
			if token != s.SessionToken || reason != session.Terminated || s.Leases != 0 || len(s.Operations) != 0 || s.Resources[session.SepgsqlSecurity] != entry {
				t.Error("resource cleanup lost terminal ownership or subject ordering")
			}
			s.Mu.Unlock()
		}
		if !e.Mu.TryLock() {
			t.Error("resource callback ran under Engine.Mu")
		} else {
			e.Mu.Unlock()
		}
		if s.TransactionState() != session.TxIdle || len(core.PayloadOf(s).Cursors.Entries) != 0 || subject.Current() != "T" || subject.Original() != "A" {
			t.Error("subject cleared before its terminal consumers drained")
		}
		beforeCleanup = subject.Generation()
		return failure
	}}); err != nil {
		t.Fatal(err)
	}
	s.RequestEnd(session.Terminated)
	if !errors.Is(op.Context().Err(), context.Canceled) {
		t.Fatal("terminal request did not cancel the active operation")
	}
	select {
	case <-s.Completed:
		t.Fatal("terminal cleanup did not wait for the active operation")
	default:
	}
	if subject.Current() != "T" {
		t.Fatal("subject cleared before operation drain")
	}
	op.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = s.WaitEnd(ctx); !errors.Is(err, failure) || calls != 1 || beforeCleanup == 0 {
		t.Fatalf("terminal cleanup error=%v consumer calls=%d generation=%d", err, calls, beforeCleanup)
	}
	sepgsqlSessionCleaned(t, s, handle, beforeCleanup+1)
	s.RequestEnd(session.CleanClose)
	if err = s.WaitEnd(ctx); !errors.Is(err, failure) || calls != 1 || s.Reason != session.Terminated {
		t.Fatalf("repeated end error=%v calls=%d reason=%d", err, calls, s.Reason)
	}
	sepgsqlSessionCleaned(t, s, handle, beforeCleanup+1)
	if err = s.Detach(handle, session.CleanClose); err != nil {
		t.Fatal("consumed resource handle was not idempotent", err)
	}
	trusted.Leave()
	subject.Finish(false)
	if subject.Current() != "" {
		t.Fatal("terminal subject retained a committed context")
	}
	core.EngineSessionMuRef(e).Lock()
	registered := core.EngineSessions(e)[s.Token()] != nil
	core.EngineSessionMuRef(e).Unlock()
	if registered || core.EngineDatabaseAdmissionCount(e) != 0 {
		t.Fatal("ended simulation session retained its registry entry or reservation")
	}
}

func TestSepgsqlSimulationInitializationCleanup(t *testing.T) {
	e := sepgsqlPhaseEngine(t)
	principal, err := e.ResolveLogin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	id := enginetest.NextSessionID()
	admission, err := e.AdmitDatabase(t.Context(), principal, id, core.DatabaseSelector{})
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Release()
	failure := errors.New("initial role provider failed after subject attachment")
	var captured *session.Session
	var handle session.ResourceHandle
	var generation uint64
	var leave func()
	provider := roleDefaultsProvider(func(_ context.Context, definition settings.Definition) (string, bool, error) {
		if definition.Name != "role" {
			return "", false, nil
		}
		// initialRole is the real startup callback after subject attachment and
		// before the transaction owner attaches. Observe, then fail that phase.
		captured = core.DatabaseAdmissionSession(admission)
		if captured == nil || captured.Sepgsql == nil {
			t.Error("initial role callback did not receive an attached subject")
			return "", false, failure
		}
		entry := captured.Resources[session.SepgsqlSecurity]
		if captured.State != session.Initializing || entry == nil || entry.Owner != captured.Sepgsql {
			t.Error("initialization did not transfer subject resource ownership")
			return "", false, failure
		}
		handle = entry.Handle
		subject := captured.Sepgsql
		subject.Set("R")
		subject.Finish(true)
		subject.Set("A")
		leave = subject.Push("T")
		generation = subject.Generation()
		return "", false, failure
	})
	s, err := e.StartSession(t.Context(), admission, session.SessionStart{ID: id, Defaults: provider})
	if s != nil || !errors.Is(err, failure) || captured == nil || generation == 0 {
		t.Fatalf("startup result=%v error=%v captured=%v generation=%d", s, err, captured != nil, generation)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = captured.WaitEnd(ctx); err != nil || captured.Reason != session.InitFailure {
		t.Fatal("startup did not join initialization unwind", err, captured.Reason)
	}
	sepgsqlSessionCleaned(t, captured, handle, generation+1)
	captured.RequestEnd(session.Terminated)
	if err = captured.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	sepgsqlSessionCleaned(t, captured, handle, generation+1)
	leave()
	captured.Sepgsql.Finish(false)
	if captured.Sepgsql.Current() != "" {
		t.Fatal("initialization unwind retained a committed context")
	}
	core.EngineSessionMuRef(e).Lock()
	registered := core.EngineSessions(e)[captured.Token()] != nil
	core.EngineSessionMuRef(e).Unlock()
	if registered || core.EngineDatabaseAdmissionCount(e) != 0 || core.DatabaseAdmissionSession(admission) != nil {
		t.Fatal("failed startup published or retained a physical reservation")
	}
	enginetest.LockEmpty(t, e)
}
