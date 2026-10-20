package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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
		key := lock.Resource{Kind: lock.AdvisoryPairResource, High: 0x80000000, Low: 0xffffffff}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryShare)
		waiter, err := advisoryUnitAcquire(t, b, rb, key, lock.AdvisoryExclusive, false)
		if err != nil || waiter == nil {
			t.Fatal(waiter, err)
		}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryShare)
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
		if _, err = advisoryUnitAcquire(t, c, rc, key, lock.AdvisoryShare, true); !errors.Is(err, lock.ErrUnavailable) {
			t.Fatal("new reader barged", err)
		}
		advisoryUnitUnlock(t, a, key, lock.AdvisoryExclusive, true)
		advisoryUnitUnlock(t, a, key, lock.AdvisoryShare, true)
		select {
		case <-waiter.Done():
			t.Fatal("partial decrement woke writer", waiter.Err())
		default:
		}
		advisoryUnitUnlock(t, a, key, lock.AdvisoryShare, true)
		if err = deadlockResult(t, waiter); err != nil || advisoryUnitHold(b, key).Exclusive != 1 {
			t.Fatal("counted queued grant", err)
		}
		advisoryUnitUnlock(t, b, key, lock.AdvisoryExclusive, true)
		advisoryUnitGrant(t, c, rc, key, lock.AdvisoryShare)
	})
	t.Run("shared upgrade cycle preserves prior grants", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 3}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryShare)
		advisoryUnitGrant(t, b, rb, key, lock.AdvisoryShare)
		first, err := advisoryUnitAcquire(t, a, ra, key, lock.AdvisoryExclusive, false)
		if err != nil || first == nil {
			t.Fatal(first, err)
		}
		victim, err := advisoryUnitAcquire(t, b, rb, key, lock.AdvisoryExclusive, false)
		if err != nil || victim == nil {
			t.Fatal(victim, err)
		}
		var d *sqlstate.Error
		if err = deadlockResult(t, victim); !errors.As(err, &d) || d.Code != "40P01" || !strings.Contains(d.Detail, "advisory lock [0,3,1]") || strings.Count(d.Detail, "blocked by session ") != 2 {
			t.Fatal("physical advisory cycle", err)
		}
		if advisoryUnitHold(a, key).Shared != 1 || advisoryUnitHold(b, key).Shared != 1 {
			t.Fatal("victim erased prior hold")
		}
		advisoryUnitUnlock(t, b, key, lock.AdvisoryShare, true)
		if err = deadlockResult(t, first); err != nil {
			t.Fatal(err)
		}
		if h := advisoryUnitHold(a, key); h.Shared != 1 || h.Exclusive != 1 {
			t.Fatal(h)
		}
		lockState := e.locks.Inspect(a.Transactions.Ticket, b.Transactions.Ticket)
		pendingJournal := len(lockState.Journals[a.Transactions.Ticket]) + len(lockState.Journals[b.Transactions.Ticket])
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
		ordinary := lock.Resource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t")}
		key := lock.Resource{Kind: lock.AdvisoryPairResource, High: 7, Low: 8}
		live := e.locks.Inspect()
		if live.Participant(a.Transactions.Ticket) != live.Participant(a.Advisory.Ticket) || live.Participant(b.Transactions.Ticket) != live.Participant(b.Advisory.Ticket) {
			t.Fatal("live tickets did not collapse")
		}
		if err := e.locks.Acquire(context.Background(), a.Transactions.Ticket, ordinary, lock.AccessExclusive); err != nil {
			t.Fatal(err)
		}
		advisoryUnitGrant(t, b, rb, key, lock.AdvisoryExclusive)
		waiting, err := advisoryUnitAcquire(t, a, ra, key, lock.AdvisoryExclusive, false)
		if err != nil || waiting == nil {
			t.Fatal(waiting, err)
		}
		victim := deadlockRequest(t, e, b.Transactions.Ticket, ordinary, lock.AccessExclusive)
		var d *sqlstate.Error
		if err = deadlockResult(t, victim); !errors.As(err, &d) || d.Code != "40P01" || !strings.Contains(d.Detail, "advisory lock [7,8,2]") || !strings.Contains(d.Detail, " on relation ") {
			t.Fatal("mixed cycle absent", err)
		}
		transactionEnd(t, b, rb, false)
		if advisoryUnitHold(b, key).Exclusive != 1 {
			t.Fatal("transaction victim removed persistent grant")
		}
		advisoryUnitUnlock(t, b, key, lock.AdvisoryExclusive, true)
		if err = deadlockResult(t, waiting); err != nil {
			t.Fatal(err)
		}
	})
	for _, statementFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "current lock timer", true: "current statement timer"}[statementFirst], func(t *testing.T) {
			e := New()
			a, ra := advisoryUnitOwner(t, e)
			b, rb := advisoryUnitOwner(t, e)
			key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 4}
			advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
			policy := lock.Policy{Provenance: rb, Timeout: 70 * time.Millisecond, StatementDeadline: time.Now().Add(time.Second)}
			code, message := "55P03", "canceling statement due to lock timeout"
			if statementFirst {
				policy.Timeout = time.Second
				policy.StatementDeadline = time.Now().Add(70 * time.Millisecond)
				code, message = "57014", "canceling statement due to statement timeout"
			}
			// Statement expiration is delivered by the admitted command's
			// context; the canonical manager itself schedules only lock_timeout.
			clock, cancel := context.WithDeadline(context.Background(), policy.StatementDeadline)
			t.Cleanup(cancel)
			policy.Source = context.Background()
			r, err := e.locks.StartAcquire(clock, b.Advisory.Ticket, key, lock.AdvisoryExclusive, policy)
			if err == nil {
				if r == nil {
					t.Fatal("blocked request absent")
				}
				err = deadlockResult(t, r)
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != "" || advisoryUnitHold(b, key) != (lock.Hold{}) {
				t.Fatal("current timer provenance", err)
			}
		})
	}
	t.Run("caller cancellation and completed callback isolation", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 5}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		r, err := e.locks.StartAcquire(ctx, b.Advisory.Ticket, key, lock.AdvisoryExclusive, lock.Policy{Provenance: rb, Source: ctx, Timeout: time.Second})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		cancel()
		if err = deadlockResult(t, r); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		advisoryUnitUnlock(t, a, key, lock.AdvisoryExclusive, true)
		advisoryUnitGrant(t, b, rb, key, lock.AdvisoryExclusive)
		if advisoryUnitHold(b, key).Exclusive != 1 {
			t.Fatal("retired cancellation poisoned successor")
		}
		deadline := time.Now()
		var d *sqlstate.Error
		if err = lock.WaitError(context.Background(), lock.Policy{StatementDeadline: deadline}, deadline, deadline); !errors.As(err, &d) || d.Code != "55P03" {
			t.Fatal("equal deadlines changed lock precedence", err)
		}
	})
	t.Run("detached prepared owner is not originating session", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		old := a.Transactions.Ticket
		ordinary := lock.Resource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t")}
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 6}
		if err := e.locks.Acquire(context.Background(), old, ordinary, lock.AccessExclusive); err != nil {
			t.Fatal(err)
		}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
		sessionSQL(t, a, "PREPARE TRANSACTION 'advisory_detached'")
		current := transactionBegin(t, a)
		lockState := e.locks.Inspect(old)
		detached := lockState.Detached[old]
		participant := lockState.Participant(old)
		same := participant == lockState.Participant(a.Transactions.Ticket)
		if !detached || participant.Prepared != ra || participant.Session != (ids.SessionToken{}) || same || advisoryUnitHold(a, key).Exclusive != 1 {
			t.Fatal("prepared identity/counts", detached, participant, same)
		}
		if _, err := e.locks.StartAcquire(context.Background(), a.Transactions.Ticket, ordinary, lock.AccessExclusive, lock.Policy{NoWait: true}); !errors.Is(err, lock.ErrUnavailable) {
			t.Fatal("prepared owner self-bypassed", err)
		}
		advisoryUnitGrant(t, a, current, key, lock.AdvisoryExclusive)
		request := deadlockRequest(t, e, a.Transactions.Ticket, ordinary, lock.AccessExclusive)
		if request == nil {
			t.Fatal("detached prepared lock was released")
		}
		sessionSQL(t, b, "COMMIT PREPARED 'advisory_detached'")
		if err := deadlockResult(t, request); err != nil {
			t.Fatal("prepared completion did not wake", err)
		}
		e.locks.DetachPrepared(old)
		lockState = e.locks.Inspect(old)
		resurrected := lockState.Owners[ra] != nil
		oldResources := len(lockState.Journals[old])
		if resurrected || oldResources != 0 || advisoryUnitHold(a, key).Exclusive != 2 {
			t.Fatal("stale detach resurrected or changed holds", resurrected, oldResources)
		}
	})
}
