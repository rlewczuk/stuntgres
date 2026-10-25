package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestAdvisorySessionCompatibleCohortTerminalOrder(t *testing.T) {
	t.Run("canceled writer admits queued shared cohort", func(t *testing.T) {
		e := engine.New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		c, rc := advisoryUnitOwner(t, e)
		d, rd := advisoryUnitOwner(t, e)
		key := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryPairResource, High: 12, Low: 13}
		prior := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryBigintResource, Low: 14}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryShare)
		advisoryUnitGrant(t, b, rb, prior, lock.AdvisoryExclusive)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		writer, err := e.Locks.StartAcquire(ctx, b.Advisory.Ticket, key, lock.AdvisoryExclusive, lock.Policy{Source: ctx, Provenance: rb})
		if err != nil || writer == nil {
			t.Fatal(writer, err)
		}
		first, err := advisoryUnitAcquire(t, c, rc, key, lock.AdvisoryShare, false)
		if err != nil || first == nil {
			t.Fatal("queued first shared", first, err)
		}
		second, err := advisoryUnitAcquire(t, d, rd, key, lock.AdvisoryShare, false)
		if err != nil || second == nil {
			t.Fatal("queued second shared", second, err)
		}
		lockState := e.Locks.Inspect()
		queued := len(lockState.Resources[key].Pending)
		if queued != 3 {
			t.Fatal("exact queue absent", queued)
		}
		cancel()
		if err = deadlockResult(t, writer); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err = deadlockResult(t, first); err != nil {
			t.Fatal(err)
		}
		if err = deadlockResult(t, second); err != nil {
			t.Fatal(err)
		}
		if advisoryUnitHold(a, key).Shared != 1 || advisoryUnitHold(c, key).Shared != 1 || advisoryUnitHold(d, key).Shared != 1 || advisoryUnitHold(b, key) != (lock.Hold{}) || advisoryUnitHold(b, prior).Exclusive != 1 {
			t.Fatal("cohort/cancellation changed prior grants")
		}
	})
	t.Run("grant precedes notification and later cancellation", func(t *testing.T) {
		e := engine.New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryBigintResource, Low: 15}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		request, err := e.Locks.StartAcquire(ctx, b.Advisory.Ticket, key, lock.AdvisoryExclusive, lock.Policy{Source: ctx, Provenance: rb, Timeout: time.Second})
		if err != nil || request == nil {
			t.Fatal(request, err)
		}
		advisoryUnitUnlock(t, a, key, lock.AdvisoryExclusive, true)
		terminal := request.Terminal()
		requestError := request.Err()
		count := e.Locks.Inspect().Resources[key].Holders[b.Advisory.Ticket].Exclusive
		if !terminal || requestError != nil || count != 1 {
			t.Fatal("grant publication boundary", terminal, requestError, count)
		}
		cancel()
		if err = deadlockResult(t, request); err != nil || advisoryUnitHold(b, key).Exclusive != 1 {
			t.Fatal("late cancellation retracted terminal grant", err)
		}
		enginetest.TransactionEnd(t, b, rb, false)
		next := enginetest.TransactionBegin(t, b)
		advisoryUnitGrant(t, b, next, key, lock.AdvisoryExclusive)
		if advisoryUnitHold(b, key).Exclusive != 2 {
			t.Fatal("late callback contaminated new incarnation")
		}
	})
}

func deadlockResult(t *testing.T, r *lock.Request) error {
	t.Helper()
	if r == nil {
		return nil
	}
	if deadlockGuardCompleted(r.Done()) {
		return r.Err()
	}
	guard := time.NewTimer(time.Second)
	defer guard.Stop()
	if !deadlockAwait(r.Done(), guard.C) {
		t.Fatal("request did not terminate before failure guard")
	}
	return r.Err()
}

func deadlockGuardCompleted(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func deadlockAwait(done <-chan struct{}, guard <-chan time.Time) bool {
	if deadlockGuardCompleted(done) {
		return true
	}
	select {
	case <-done:
		return true
	case <-guard:
		return deadlockGuardCompleted(done)
	}
}
