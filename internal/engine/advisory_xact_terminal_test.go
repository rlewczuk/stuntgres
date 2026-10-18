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
				key := lockResource{kind: advisoryPairResource, high: 19, low: 20}
				advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				request, err := m.startAcquire(ctx, bt, key, advisoryShared, relationLockPolicy{timeout: time.Hour})
				if err != nil || request == nil {
					t.Fatal(request, err)
				}
				terminate := func() {
					switch terminal {
					case "cancel":
						cancel()
					case "end":
						m.end(bt)
					case "timeout":
						m.mu.Lock()
						request.deadline = time.Now().Add(-time.Second)
						var notify *relationRequest
						if !request.terminal {
							m.settle(request.entry, &notify)
							m.retire(key, request.entry)
						}
						m.mu.Unlock()
						notifyRequests(notify)
					case "fence":
						m.fence(bt.ref.Epoch() + 1)
					}
				}
				if grantFirst {
					m.end(at)
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
					m.end(at)
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
				if request.timer.Stop() {
					t.Fatal("terminal left lock timer armed")
				}
				if request.stop() {
					t.Fatal("terminal left cancellation registration armed")
				}
				m.end(bt)
				m.mu.Lock()
				entry := m.entries[key]
				leaked := entry != nil && (len(entry.holders) != 0 || len(entry.pending) != 0)
				journal := len(bt.resources)
				m.mu.Unlock()
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
		key := lockResource{kind: advisoryBigintResource, low: 66}
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		ctx, cancel := context.WithCancel(context.Background())
		request, err := e.locks.startAcquire(ctx, b.transactions.ticket, key, advisoryExclusive, relationLockPolicy{timeout: time.Hour})
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
		held := advisoryXactUnitHold(b, key).mode
		if err == nil && held != advisoryExclusive || err != nil && (!errors.Is(err, context.Canceled) || held != 0) {
			t.Fatal("inconsistent terminal publication", err, held)
		}
		e.endLockOwner(b.transactions.ticket)
	}
}
