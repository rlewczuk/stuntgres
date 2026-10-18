package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/sqlstate"
	"sync"
	"testing"
	"time"
)

func TestAdvisoryXactTerminalRaces(t *testing.T) {
	for _, terminal := range []string{"cancel", "end", "timeout", "fence"} {
		for _, grantFirst := range []bool{false, true} {
			t.Run(fmt.Sprint(terminal, grantFirst), func(t *testing.T) {
				e := New()
				a, _ := advisoryUnitOwner(t, e)
				b, _ := advisoryUnitOwner(t, e)
				m := e.locks
				at, bt := a.transactions.ticket, b.transactions.ticket
				key := lockResource{Kind: advisoryPairResource, High: 19, Low: 20}
				advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				request, err := m.StartAcquire(ctx, bt, key, advisoryShared, relationLockPolicy{Timeout: time.Hour})
				if err != nil || request == nil {
					t.Fatal(request, err)
				}
				terminate := func() {
					switch terminal {
					case "cancel":
						cancel()
					case "end":
						m.End(bt)
					case "timeout":
						m.ExpireWait(request, request.Deadline())
					case "fence":
						m.Fence(bt.Ref().Epoch() + 1)
					}
				}
				if grantFirst {
					m.End(at)
					if err := deadlockResult(t, request); err != nil {
						t.Fatal(err)
					}
					terminate()
				} else {
					terminate()
					if terminal == "cancel" {
						if err := deadlockResult(t, request); !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					}
					m.End(at)
				}
				err = deadlockResult(t, request)
				if grantFirst {
					if err != nil {
						t.Fatal("terminal changed published grant", err)
					}
				} else {
					switch terminal {
					case "cancel":
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					case "end":
						if !errors.Is(err, errRelationOwnerEnded) {
							t.Fatal(err)
						}
					case "fence":
						if !errors.Is(err, ErrCatalogReplaced) {
							t.Fatal(err)
						}
					case "timeout":
						var d *sqlstate.Error
						if !errors.As(err, &d) || d.Code != "55P03" {
							t.Fatal(err)
						}
					}
				}
				if !request.Released() {
					t.Fatal("terminal left lock timer or cancellation registration armed")
				}
				m.End(bt)
				lockState := m.Inspect(bt)
				entry := lockState.Resources[key]
				leaked := entry != nil && (len(entry.Holders) != 0 || len(entry.Pending) != 0)
				journal := len(lockState.Journals[bt])
				if leaked || journal != 0 {
					t.Fatal("terminal leaked ownership", leaked, journal)
				}
			})
		}
	}
	// Exercise a genuinely simultaneous cancel/grant boundary. Either serialized
	// winner is valid, but the published outcome must agree with the held modes.
	for range 16 {
		e := New()
		a, _ := advisoryUnitOwner(t, e)
		b, _ := advisoryUnitOwner(t, e)
		key := lockResource{Kind: advisoryBigintResource, Low: 66}
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		ctx, cancel := context.WithCancel(context.Background())
		request, err := e.locks.StartAcquire(ctx, b.transactions.ticket, key, advisoryExclusive, relationLockPolicy{Timeout: time.Hour})
		if err != nil || request == nil {
			cancel()
			t.Fatal(request, err)
		}
		start := make(chan struct{})
		var joined sync.WaitGroup
		joined.Add(2)
		go func() { defer joined.Done(); <-start; cancel() }()
		go func() { defer joined.Done(); <-start; e.endLockOwner(a.transactions.ticket) }()
		close(start)
		joined.Wait()
		err = deadlockResult(t, request)
		held := advisoryXactUnitHold(b, key).Mode
		if err == nil && held != advisoryExclusive || err != nil && (!errors.Is(err, context.Canceled) || held != 0) {
			t.Fatal("inconsistent terminal publication", err, held)
		}
		e.endLockOwner(b.transactions.ticket)
	}
}
