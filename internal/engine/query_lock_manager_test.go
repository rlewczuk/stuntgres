package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestQueryLockConditionalRowManager(t *testing.T) {
	modes := []lock.Mode{lock.ForKeyShare, lock.ForShare, lock.ForNoKeyUpdate, lock.ForUpdate}
	for _, held := range modes {
		for _, requested := range modes {
			t.Run(queryRowClause(held)+"/"+queryRowClause(requested), func(t *testing.T) {
				e, a, b, _ := rowLockEngineFixture(t)
				ra := transactionBegin(t, a)
				t.Cleanup(func() { transactionEnd(t, a, ra, false) })
				rb := transactionBegin(t, b)
				t.Cleanup(func() { transactionEnd(t, b, rb, false) })
				ref := roleTableRef(t, e, "t")
				e.mu.Lock()
				row := e.storage.Relations[ref.Address].Versions[0].Row
				e.mu.Unlock()
				resource := lock.Resource{ObjectRef: ref, Row: row}
				if err := e.locks.Acquire(context.Background(), a.transactions.ticket, resource, held); err != nil {
					t.Fatal(err)
				}
				before := len(e.locks.Inspect(b.transactions.ticket).Journals[b.transactions.ticket])
				request, err := e.locks.StartAcquire(context.Background(), b.transactions.ticket, resource, requested, lock.Policy{NoWait: true, Timeout: time.Hour})
				conflict := lock.Conflicts(requested, held)
				if request != nil || conflict && !errors.Is(err, lock.ErrUnavailable) || !conflict && err != nil {
					t.Fatal(request, err, conflict)
				}
				lockState := e.locks.Inspect(b.transactions.ticket)
				entry := lockState.Resources[resource]
				bad := len(entry.Pending) != 0 || entry.Holders[a.transactions.ticket].Mode != held
				if conflict {
					bad = bad || entry.Holders[b.transactions.ticket].Mode != 0 || len(lockState.Journals[b.transactions.ticket]) != before
				} else {
					bad = bad || entry.Holders[b.transactions.ticket].Mode != requested
				}
				if bad {
					t.Fatal("conditional row acquisition changed unrelated hold/queue or journaled failure")
				}
			})
		}
	}
}

func TestQueryLockConditionalQueueAndSelfReentry(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	ra := transactionBegin(t, a)
	t.Cleanup(func() { transactionEnd(t, a, ra, false) })
	rb := transactionBegin(t, b)
	t.Cleanup(func() { transactionEnd(t, b, rb, false) })
	rc := transactionBegin(t, c)
	t.Cleanup(func() { transactionEnd(t, c, rc, false) })
	ref := roleTableRef(t, e, "t")
	e.mu.Lock()
	row := e.storage.Relations[ref.Address].Versions[0].Row
	e.mu.Unlock()
	resource := lock.Resource{ObjectRef: ref, Row: row}
	if err := e.locks.Acquire(context.Background(), a.transactions.ticket, resource, lock.ForKeyShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waiter, err := e.locks.StartAcquire(ctx, b.transactions.ticket, resource, lock.ForUpdate, lock.Policy{})
	if err != nil || waiter == nil {
		t.Fatal("missing exact incompatible waiter", waiter, err)
	}
	before := len(e.locks.Inspect(c.transactions.ticket).Journals[c.transactions.ticket])
	probe, err := e.locks.StartAcquire(context.Background(), c.transactions.ticket, resource, lock.ForKeyShare, lock.Policy{NoWait: true})
	if probe != nil || !errors.Is(err, lock.ErrUnavailable) {
		t.Fatal("compatible newcomer barged past earlier UPDATE", probe, err)
	}
	self, err := e.locks.StartAcquire(context.Background(), a.transactions.ticket, resource, lock.ForShare, lock.Policy{NoWait: true})
	if self != nil || err != nil {
		t.Fatal("self upgrade joined its own blocked queue", self, err)
	}
	lockState := e.locks.Inspect(c.transactions.ticket)
	entry := lockState.Resources[resource]
	bad := len(entry.Pending) != 1 || entry.Pending[0].Request != waiter || entry.Holders[a.transactions.ticket].Mode != (lock.ForKeyShare|lock.ForShare) || len(lockState.Journals[c.transactions.ticket]) != before
	if bad {
		t.Fatal("conditional probe altered queue/journal/self contributions")
	}
	cancel()
	if err := waiter.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	probe, err = e.locks.StartAcquire(context.Background(), c.transactions.ticket, resource, lock.ForKeyShare, lock.Policy{NoWait: true})
	if probe != nil || err != nil {
		t.Fatal("cancelled predecessor left ghost conditional denial", probe, err)
	}
}
