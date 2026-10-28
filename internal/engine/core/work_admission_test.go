package core

import (
	"context"
	"errors"
	"sync"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/workadmission"
)

func TestWorkAdmissionRuntimeReservationHandoff(t *testing.T) {
	e := &Engine{Mu: new(sync.Mutex)}
	sess := &session.Session{Host: (*sessionHost)(e)}
	s := &StatementScope{Op: &session.Operation{Session: sess}, Owner: &session.TransactionOwner{}}
	s.self = s
	refusal := errors.New("credit refused")
	ctx := workadmission.With(context.Background(), func(context.Context, workadmission.Class) error {
		if !e.ReplacementMu.TryLock() {
			t.Fatal("application callback holds replacement reservation")
		}
		e.ReplacementMu.Unlock()
		if !e.Mu.TryLock() {
			t.Fatal("application callback holds engine mutex")
		}
		e.Mu.Unlock()
		if !sess.Mu.TryLock() {
			t.Fatal("application callback holds session mutex")
		}
		sess.Mu.Unlock()
		if !s.Owner.Mu.TryLock() {
			t.Fatal("application callback holds transaction state mutex")
		}
		s.Owner.Mu.Unlock()
		return refusal
	})
	e.ReplacementMu.RLock()
	s.ReplacementRead = true
	active := s.activateWork(ctx)
	if s.ReplacementRead {
		t.Fatal("runtime startup did not hand off reservation")
	}
	// Real row-lock waits reacquire the reservation. A later work quantum must
	// perform the handoff again, not rely on the startup release.
	e.ReplacementMu.RLock()
	s.ReplacementRead = true
	if err := workadmission.Admit(active, workadmission.WorkPatternMatch); err != refusal {
		t.Fatal("handoff changed refusal identity", err)
	}
	if s.ReplacementRead {
		t.Fatal("later quantum retained reacquired reservation")
	}
	if err := workadmission.Admit(ctx, workadmission.WorkPatternMatch); err != nil {
		t.Fatal("runtime activation mutated original context", err)
	}
}

func TestWorkAdmissionRuntimeRejectsRetiredOwner(t *testing.T) {
	e := &Engine{Mu: new(sync.Mutex)}
	sess := &session.Session{Host: (*sessionHost)(e)}
	s := &StatementScope{Op: &session.Operation{Session: sess}, Owner: &session.TransactionOwner{}, Finished: true}
	s.self = s
	active := s.activateWork(workadmission.With(context.Background(), func(context.Context, workadmission.Class) error { return nil }))
	if err := workadmission.Admit(active, workadmission.WorkRecursiveUnion); err == nil {
		t.Fatal("application grant revived retired statement owner")
	}
}

func TestWorkAdmissionRuntimeDisabledAllocations(t *testing.T) {
	var s *StatementScope
	ctx := context.Background()
	if allocations := testing.AllocsPerRun(100, func() {
		if s.activateWork(ctx) != ctx {
			t.Fatal("disabled runtime changed context")
		}
	}); allocations != 0 {
		t.Fatal("disabled runtime allocated", allocations)
	}
}
