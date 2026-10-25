package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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
				m := e.Locks
				at, bt := a.Transactions.Ticket, b.Transactions.Ticket
				key := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryPairResource, High: 19, Low: 20}
				advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				request, err := m.StartAcquire(ctx, bt, key, lock.AdvisoryShare, lock.Policy{Timeout: time.Hour})
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
						m.Fence(bt.Ref().Domain(), bt.Ref().Epoch()+1)
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
						if !errors.Is(err, lock.ErrOwnerEnded) {
							t.Fatal(err)
						}
					case "fence":
						if !errors.Is(err, ids.ErrCatalogReplaced) {
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
		key := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryBigintResource, Low: 66}
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
		ctx, cancel := context.WithCancel(context.Background())
		request, err := e.Locks.StartAcquire(ctx, b.Transactions.Ticket, key, lock.AdvisoryExclusive, lock.Policy{Timeout: time.Hour})
		if err != nil || request == nil {
			cancel()
			t.Fatal(request, err)
		}
		start := make(chan struct{})
		var joined sync.WaitGroup
		joined.Add(2)
		go func() { defer joined.Done(); <-start; cancel() }()
		go func() { defer joined.Done(); <-start; e.EndLockOwner(a.Transactions.Ticket) }()
		close(start)
		joined.Wait()
		err = deadlockResult(t, request)
		held := advisoryXactUnitHold(b, key).Mode
		if err == nil && held != lock.AdvisoryExclusive || err != nil && (!errors.Is(err, context.Canceled) || held != 0) {
			t.Fatal("inconsistent terminal publication", err, held)
		}
		e.EndLockOwner(b.Transactions.Ticket)
	}
}
