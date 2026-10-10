package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestAdvisorySessionQueueTimersDeadlock(t *testing.T) {
	t.Run("owner priority and nonbarging", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		c, rc := advisoryUnitOwner(t, e)
		key := lockResource{kind: advisoryPairResource, high: 0x80000000, low: 0xffffffff}
		advisoryUnitGrant(t, a, ra, key, advisoryShared)
		waiter, err := advisoryUnitAcquire(t, b, rb, key, advisoryExclusive, false)
		if err != nil || waiter == nil {
			t.Fatal(waiter, err)
		}
		advisoryUnitGrant(t, a, ra, key, advisoryShared)
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		if _, err = advisoryUnitAcquire(t, c, rc, key, advisoryShared, true); !errors.Is(err, errRelationUnavailable) {
			t.Fatal("new reader barged", err)
		}
		advisoryUnitUnlock(t, a, key, advisoryExclusive, true)
		advisoryUnitUnlock(t, a, key, advisoryShared, true)
		select {
		case <-waiter.done:
			t.Fatal("partial decrement woke writer", waiter.err)
		default:
		}
		advisoryUnitUnlock(t, a, key, advisoryShared, true)
		if err = deadlockResult(t, waiter); err != nil || advisoryUnitHold(b, key).exclusive != 1 {
			t.Fatal("counted queued grant", err)
		}
		advisoryUnitUnlock(t, b, key, advisoryExclusive, true)
		advisoryUnitGrant(t, c, rc, key, advisoryShared)
	})
	t.Run("shared upgrade cycle preserves prior grants", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lockResource{kind: advisoryBigintResource, low: 3}
		advisoryUnitGrant(t, a, ra, key, advisoryShared)
		advisoryUnitGrant(t, b, rb, key, advisoryShared)
		first, err := advisoryUnitAcquire(t, a, ra, key, advisoryExclusive, false)
		if err != nil || first == nil {
			t.Fatal(first, err)
		}
		victim, err := advisoryUnitAcquire(t, b, rb, key, advisoryExclusive, false)
		if err != nil || victim == nil {
			t.Fatal(victim, err)
		}
		var d *sqlstate.Error
		if err = deadlockResult(t, victim); !errors.As(err, &d) || d.Code != "40P01" || !strings.Contains(d.Detail, "advisory lock [0,3,1]") || strings.Count(d.Detail, "blocked by session ") != 2 {
			t.Fatal("physical advisory cycle", err)
		}
		if advisoryUnitHold(a, key).shared != 1 || advisoryUnitHold(b, key).shared != 1 {
			t.Fatal("victim erased prior hold")
		}
		advisoryUnitUnlock(t, b, key, advisoryShared, true)
		if err = deadlockResult(t, first); err != nil {
			t.Fatal(err)
		}
		if h := advisoryUnitHold(a, key); h.shared != 1 || h.exclusive != 1 {
			t.Fatal(h)
		}
		e.locks.mu.Lock()
		pendingJournal := len(a.transactions.ticket.resources) + len(b.transactions.ticket.resources)
		e.locks.mu.Unlock()
		if pendingJournal != 0 {
			t.Fatal("terminal provenance journal retained", pendingJournal)
		}
	})
	t.Run("mixed physical session cycle", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		rb := transactionBegin(t, b)
		ordinary := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t")}
		key := lockResource{kind: advisoryPairResource, high: 7, low: 8}
		if a.transactions.ticket.participant() != a.advisory.ticket.participant() || b.transactions.ticket.participant() != b.advisory.ticket.participant() {
			t.Fatal("live tickets did not collapse")
		}
		if err := e.locks.acquire(context.Background(), a.transactions.ticket, ordinary, accessExclusive); err != nil {
			t.Fatal(err)
		}
		advisoryUnitGrant(t, b, rb, key, advisoryExclusive)
		waiting, err := advisoryUnitAcquire(t, a, ra, key, advisoryExclusive, false)
		if err != nil || waiting == nil {
			t.Fatal(waiting, err)
		}
		victim := deadlockRequest(t, e, b.transactions.ticket, ordinary, accessExclusive)
		var d *sqlstate.Error
		if err = deadlockResult(t, victim); !errors.As(err, &d) || d.Code != "40P01" || !strings.Contains(d.Detail, "advisory lock [7,8,2]") || !strings.Contains(d.Detail, " on relation ") {
			t.Fatal("mixed cycle absent", err)
		}
		transactionEnd(t, b, rb, false)
		if advisoryUnitHold(b, key).exclusive != 1 {
			t.Fatal("transaction victim removed persistent grant")
		}
		advisoryUnitUnlock(t, b, key, advisoryExclusive, true)
		if err = deadlockResult(t, waiting); err != nil {
			t.Fatal(err)
		}
	})
	for _, statementFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "current lock timer", true: "current statement timer"}[statementFirst], func(t *testing.T) {
			e := New()
			a, ra := advisoryUnitOwner(t, e)
			b, rb := advisoryUnitOwner(t, e)
			key := lockResource{kind: advisoryBigintResource, low: 4}
			advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
			policy := relationLockPolicy{provenance: rb, timeout: 70 * time.Millisecond, statementDeadline: time.Now().Add(time.Second)}
			code, message := "55P03", "canceling statement due to lock timeout"
			if statementFirst {
				policy.timeout = time.Second
				policy.statementDeadline = time.Now().Add(70 * time.Millisecond)
				code, message = "57014", "canceling statement due to statement timeout"
			}
			// Statement expiration is delivered by the admitted command's
			// context; the canonical manager itself schedules only lock_timeout.
			clock, cancel := context.WithDeadline(context.Background(), policy.statementDeadline)
			t.Cleanup(cancel)
			policy.source = context.Background()
			r, err := e.locks.startAcquire(clock, b.advisory.ticket, key, advisoryExclusive, policy)
			if err == nil {
				if r == nil {
					t.Fatal("blocked request absent")
				}
				err = deadlockResult(t, r)
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != "" || advisoryUnitHold(b, key) != (relationHold{}) {
				t.Fatal("current timer provenance", err)
			}
		})
	}
	t.Run("caller cancellation and completed callback isolation", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lockResource{kind: advisoryBigintResource, low: 5}
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		r, err := e.locks.startAcquire(ctx, b.advisory.ticket, key, advisoryExclusive, relationLockPolicy{provenance: rb, source: ctx, timeout: time.Second})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		cancel()
		if err = deadlockResult(t, r); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		advisoryUnitUnlock(t, a, key, advisoryExclusive, true)
		advisoryUnitGrant(t, b, rb, key, advisoryExclusive)
		if advisoryUnitHold(b, key).exclusive != 1 {
			t.Fatal("retired cancellation poisoned successor")
		}
		deadline := time.Now()
		var d *sqlstate.Error
		if err = relationWaitError(context.Background(), relationLockPolicy{statementDeadline: deadline}, deadline, deadline); !errors.As(err, &d) || d.Code != "55P03" {
			t.Fatal("equal deadlines changed lock precedence", err)
		}
	})
	t.Run("detached prepared owner is not originating session", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		old := a.transactions.ticket
		ordinary := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t")}
		key := lockResource{kind: advisoryBigintResource, low: 6}
		if err := e.locks.acquire(context.Background(), old, ordinary, accessExclusive); err != nil {
			t.Fatal(err)
		}
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		sessionSQL(t, a, "PREPARE TRANSACTION 'advisory_detached'")
		current := transactionBegin(t, a)
		e.locks.mu.Lock()
		detached := old.detached
		participant := old.participant()
		same := sameLockSession(old, a.transactions.ticket)
		e.locks.mu.Unlock()
		if !detached || participant.prepared != ra || participant.session != (SessionToken{}) || same || advisoryUnitHold(a, key).exclusive != 1 {
			t.Fatal("prepared identity/counts", detached, participant, same)
		}
		if _, err := e.locks.startAcquire(context.Background(), a.transactions.ticket, ordinary, accessExclusive, relationLockPolicy{nowait: true}); !errors.Is(err, errRelationUnavailable) {
			t.Fatal("prepared owner self-bypassed", err)
		}
		advisoryUnitGrant(t, a, current, key, advisoryExclusive)
		request := deadlockRequest(t, e, a.transactions.ticket, ordinary, accessExclusive)
		if request == nil {
			t.Fatal("detached prepared lock was released")
		}
		sessionSQL(t, b, "COMMIT PREPARED 'advisory_detached'")
		if err := deadlockResult(t, request); err != nil {
			t.Fatal("prepared completion did not wake", err)
		}
		e.locks.detachPrepared(old)
		e.locks.mu.Lock()
		resurrected := e.locks.owners[ra] != nil
		oldResources := len(old.resources)
		e.locks.mu.Unlock()
		if resurrected || oldResources != 0 || advisoryUnitHold(a, key).exclusive != 2 {
			t.Fatal("stale detach resurrected or changed holds", resurrected, oldResources)
		}
	})
}
