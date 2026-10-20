package engine

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestSessionTimeoutPendingGrantRace(t *testing.T) {
	for attempt := range 16 {
		t.Run(strconv.Itoa(attempt), func(t *testing.T) {
			s, holder, ref, lock := timeoutPendingFixture(t, false)
			op := timeoutPendingOperation(t, s)
			scope := timeoutPendingScope(t, op)
			op.lockTimeout = time.Hour
			returned, workerDone := make(chan error, 1), make(chan struct{})
			go func() {
				defer close(workerDone)
				_, err := scope.Execute(lock, 0, nil)
				returned <- err
			}()
			t.Cleanup(func() {
				op.cancel(nil)
				select {
				case <-workerDone:
					if !scope.finished {
						scope.Finish(context.Canceled)
					}
				case <-time.After(5 * time.Second):
					t.Error("racing request failed bounded join")
				}
			})
			timeoutPendingRequest(t, engineOf(s), op)
			peer := timeoutPendingOperation(t, holder)
			peerRef := holder.transactions.ref
			start, callbacksDone := make(chan struct{}), make(chan struct{})
			expired, released := make(chan error, 1), make(chan error, 1)
			var callbacks sync.WaitGroup
			callbacks.Add(2)
			go func() {
				defer callbacks.Done()
				<-start
				s.mu.Lock()
				slot := &s.timeouts.transaction
				s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", slot.deadline.Add(time.Minute))
				s.mu.Unlock()
				expired <- nil
			}()
			go func() {
				defer callbacks.Done()
				<-start
				_, err := engineOf(holder).Rollback(peer, peerRef)
				released <- err
			}()
			go func() { callbacks.Wait(); close(callbacksDone) }()
			t.Cleanup(func() {
				select {
				case <-callbacksDone:
				case <-time.After(5 * time.Second):
					t.Error("arbitration callbacks failed bounded join")
				}
			})
			close(start)
			timeoutPendingReceive(t, expired)
			if err := timeoutPendingReceive(t, released); err != nil {
				t.Fatal(err)
			}
			peer.Done()
			result := timeoutPendingReceive(t, returned)
			s.mu.Lock()
			survived := s.state == live
			if survived {
				if !s.timeouts.transaction.deadline.IsZero() || s.timeouts.fatal != nil {
					t.Error("SQL ERROR claim did not stop budget without FATAL")
				}
			} else if s.state != ending || s.timeouts.fatal == nil || s.timeouts.fatal.Code != "25P04" {
				t.Error("grant winner did not retain exact genuine FATAL")
			}
			s.mu.Unlock()
			result = scope.Finish(result)
			if survived {
				timeoutPendingDiagnostic(t, op.CommandError(result), "55P03")
			} else {
				timeoutPendingDiagnostic(t, op.CommandError(result), "25P04")
			}
			if op.lockWait.Request() != nil || op.lockWait.Failed() {
				t.Error("race completion retained timeout projection")
			}
			op.Done()
			if survived {
				timeoutPendingRecovery(t, s, ref, false)
			}
		})
	}
}

func TestSessionTimeoutPendingImmediateProjection(t *testing.T) {
	s, holder, _, _ := timeoutPendingFixture(t, false)
	manager := engineOf(s).locks
	ticket := holder.transactions.ticket
	var resource lock.Resource
	for ref := range manager.Inspect().Journals[ticket] {
		resource = ref
		break
	}
	if resource.ObjectRef == (ids.ObjectRef{}) {
		t.Fatal("holder lacks actual relation resource")
	}
	op := timeoutPendingOperation(t, holder)
	for _, policy := range []lock.Policy{
		{TimeoutSlot: &op.lockWait},
		{TimeoutSlot: &op.lockWait, Timeout: time.Hour, StatementDeadline: time.Now().Add(time.Hour)},
		{TimeoutSlot: &op.lockWait, NoWait: true, Timeout: time.Hour},
	} {
		if allocations := testing.AllocsPerRun(100, func() {
			request, err := manager.StartAcquire(op.Context(), ticket, resource, lock.AccessExclusive, policy)
			if request != nil || err != nil {
				panic("self acquisition was not immediate")
			}
		}); allocations != 0 {
			t.Fatal("immediate timeout projection allocated", allocations)
		}
		if op.lockWait.Request() != nil || op.lockWait.Failed() {
			t.Fatal("immediate grant published pending timeout")
		}
	}
	blocked := timeoutPendingOperation(t, s)
	policy := lock.Policy{TimeoutSlot: &blocked.lockWait, NoWait: true, Timeout: time.Hour}
	if allocations := testing.AllocsPerRun(100, func() {
		request, err := manager.StartAcquire(blocked.Context(), s.transactions.ticket, resource, lock.AccessShare, policy)
		if request != nil || err != lock.ErrUnavailable {
			panic("NOWAIT queued or granted conflicting request")
		}
	}); allocations != 0 {
		t.Fatal("NOWAIT timeout projection allocated", allocations)
	}
	if blocked.lockWait.Request() != nil || blocked.lockWait.Failed() {
		t.Fatal("NOWAIT published pending timeout")
	}
	request, err := manager.StartAcquire(blocked.Context(), s.transactions.ticket, resource, lock.AccessShare, lock.Policy{TimeoutSlot: &blocked.lockWait})
	if request == nil || err != nil {
		t.Fatal("disabled timeout request", request, err)
	}
	if blocked.lockWait.Request() != nil || blocked.lockWait.Failed() {
		t.Fatal("disabled timeout published pending projection")
	}
	result, done := make(chan error, 1), make(chan struct{})
	go func() { defer close(done); result <- request.Wait() }()
	t.Cleanup(func() {
		blocked.cancel(nil)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("disabled request failed bounded join")
		}
	})
	blocked.cancel(nil)
	if err := timeoutPendingReceive(t, result); err != context.Canceled {
		t.Fatal("disabled timeout cancellation", err)
	}
}
