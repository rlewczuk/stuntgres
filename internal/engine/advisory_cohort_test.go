package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdvisorySessionCompatibleCohortTerminalOrder(t *testing.T) {
	t.Run("canceled writer admits queued shared cohort", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		c, rc := advisoryUnitOwner(t, e)
		d, rd := advisoryUnitOwner(t, e)
		key := lockResource{Kind: advisoryPairResource, High: 12, Low: 13}
		prior := lockResource{Kind: advisoryBigintResource, Low: 14}
		advisoryUnitGrant(t, a, ra, key, advisoryShared)
		advisoryUnitGrant(t, b, rb, prior, advisoryExclusive)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		writer, err := e.locks.StartAcquire(ctx, b.advisory.ticket, key, advisoryExclusive, relationLockPolicy{Source: ctx, Provenance: rb})
		if err != nil || writer == nil {
			t.Fatal(writer, err)
		}
		first, err := advisoryUnitAcquire(t, c, rc, key, advisoryShared, false)
		if err != nil || first == nil {
			t.Fatal("queued first shared", first, err)
		}
		second, err := advisoryUnitAcquire(t, d, rd, key, advisoryShared, false)
		if err != nil || second == nil {
			t.Fatal("queued second shared", second, err)
		}
		lockState := e.locks.Inspect()
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
		if advisoryUnitHold(a, key).Shared != 1 || advisoryUnitHold(c, key).Shared != 1 || advisoryUnitHold(d, key).Shared != 1 || advisoryUnitHold(b, key) != (relationHold{}) || advisoryUnitHold(b, prior).Exclusive != 1 {
			t.Fatal("cohort/cancellation changed prior grants")
		}
	})
	t.Run("grant precedes notification and later cancellation", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lockResource{Kind: advisoryBigintResource, Low: 15}
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		request, err := e.locks.StartAcquire(ctx, b.advisory.ticket, key, advisoryExclusive, relationLockPolicy{Source: ctx, Provenance: rb, Timeout: time.Second})
		if err != nil || request == nil {
			t.Fatal(request, err)
		}
		advisoryUnitUnlock(t, a, key, advisoryExclusive, true)
		terminal := request.Terminal()
		requestError := request.Err()
		count := e.locks.Inspect().Resources[key].Holders[b.advisory.ticket].Exclusive
		if !terminal || requestError != nil || count != 1 {
			t.Fatal("grant publication boundary", terminal, requestError, count)
		}
		cancel()
		if err = deadlockResult(t, request); err != nil || advisoryUnitHold(b, key).Exclusive != 1 {
			t.Fatal("late cancellation retracted terminal grant", err)
		}
		transactionEnd(t, b, rb, false)
		next := transactionBegin(t, b)
		advisoryUnitGrant(t, b, next, key, advisoryExclusive)
		if advisoryUnitHold(b, key).Exclusive != 2 {
			t.Fatal("late callback contaminated new incarnation")
		}
	})
}
