package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/lock"
)

func TestQueryLockConditionalRowManager(t *testing.T) {
	modes := []lock.Mode{lock.ForKeyShare, lock.ForShare, lock.ForNoKeyUpdate, lock.ForUpdate}
	for _, held := range modes {
		for _, requested := range modes {
			t.Run(analyze.RowLockClause(held)+"/"+analyze.RowLockClause(requested), func(t *testing.T) {
				e, a, b, _ := rowLockEngineFixture(t)
				ra := transactionBegin(t, a)
				t.Cleanup(func() { transactionEnd(t, a, ra, false) })
				rb := transactionBegin(t, b)
				t.Cleanup(func() { transactionEnd(t, b, rb, false) })
				ref := roleTableRef(t, e, "t")
				e.Mu.Lock()
				row := e.Storage.Relations[ref.Address].Versions[0].Row
				e.Mu.Unlock()
				resource := lock.Resource{ObjectRef: ref, Row: row}
				if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, resource, held); err != nil {
					t.Fatal(err)
				}
				before := len(e.Locks.Inspect(b.Transactions.Ticket).Journals[b.Transactions.Ticket])
				request, err := e.Locks.StartAcquire(context.Background(), b.Transactions.Ticket, resource, requested, lock.Policy{NoWait: true, Timeout: time.Hour})
				conflict := lock.Conflicts(requested, held)
				if request != nil || conflict && !errors.Is(err, lock.ErrUnavailable) || !conflict && err != nil {
					t.Fatal(request, err, conflict)
				}
				lockState := e.Locks.Inspect(b.Transactions.Ticket)
				entry := lockState.Resources[resource]
				bad := len(entry.Pending) != 0 || entry.Holders[a.Transactions.Ticket].Mode != held
				if conflict {
					bad = bad || entry.Holders[b.Transactions.Ticket].Mode != 0 || len(lockState.Journals[b.Transactions.Ticket]) != before
				} else {
					bad = bad || entry.Holders[b.Transactions.Ticket].Mode != requested
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
	e.Mu.Lock()
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	e.Mu.Unlock()
	resource := lock.Resource{ObjectRef: ref, Row: row}
	if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, resource, lock.ForKeyShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waiter, err := e.Locks.StartAcquire(ctx, b.Transactions.Ticket, resource, lock.ForUpdate, lock.Policy{})
	if err != nil || waiter == nil {
		t.Fatal("missing exact incompatible waiter", waiter, err)
	}
	before := len(e.Locks.Inspect(c.Transactions.Ticket).Journals[c.Transactions.Ticket])
	probe, err := e.Locks.StartAcquire(context.Background(), c.Transactions.Ticket, resource, lock.ForKeyShare, lock.Policy{NoWait: true})
	if probe != nil || !errors.Is(err, lock.ErrUnavailable) {
		t.Fatal("compatible newcomer barged past earlier UPDATE", probe, err)
	}
	self, err := e.Locks.StartAcquire(context.Background(), a.Transactions.Ticket, resource, lock.ForShare, lock.Policy{NoWait: true})
	if self != nil || err != nil {
		t.Fatal("self upgrade joined its own blocked queue", self, err)
	}
	lockState := e.Locks.Inspect(c.Transactions.Ticket)
	entry := lockState.Resources[resource]
	bad := len(entry.Pending) != 1 || entry.Pending[0].Request != waiter || entry.Holders[a.Transactions.Ticket].Mode != (lock.ForKeyShare|lock.ForShare) || len(lockState.Journals[c.Transactions.Ticket]) != before
	if bad {
		t.Fatal("conditional probe altered queue/journal/self contributions")
	}
	cancel()
	if err := waiter.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	probe, err = e.Locks.StartAcquire(context.Background(), c.Transactions.Ticket, resource, lock.ForKeyShare, lock.Policy{NoWait: true})
	if probe != nil || err != nil {
		t.Fatal("cancelled predecessor left ghost conditional denial", probe, err)
	}
}
