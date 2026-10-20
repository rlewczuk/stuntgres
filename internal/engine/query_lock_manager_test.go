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
				e.mu.Lock()
				row := e.storage.Relations[ref.Address].Versions[0].Row
				e.mu.Unlock()
				resource := lock.Resource{ObjectRef: ref, Row: row}
				if err := e.locks.Acquire(context.Background(), a.Transactions.ticket, resource, held); err != nil {
					t.Fatal(err)
				}
				before := len(e.locks.Inspect(b.Transactions.ticket).Journals[b.Transactions.ticket])
				request, err := e.locks.StartAcquire(context.Background(), b.Transactions.ticket, resource, requested, lock.Policy{NoWait: true, Timeout: time.Hour})
				conflict := lock.Conflicts(requested, held)
				if request != nil || conflict && !errors.Is(err, lock.ErrUnavailable) || !conflict && err != nil {
					t.Fatal(request, err, conflict)
				}
				lockState := e.locks.Inspect(b.Transactions.ticket)
				entry := lockState.Resources[resource]
				bad := len(entry.Pending) != 0 || entry.Holders[a.Transactions.ticket].Mode != held
				if conflict {
					bad = bad || entry.Holders[b.Transactions.ticket].Mode != 0 || len(lockState.Journals[b.Transactions.ticket]) != before
				} else {
					bad = bad || entry.Holders[b.Transactions.ticket].Mode != requested
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
	if err := e.locks.Acquire(context.Background(), a.Transactions.ticket, resource, lock.ForKeyShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waiter, err := e.locks.StartAcquire(ctx, b.Transactions.ticket, resource, lock.ForUpdate, lock.Policy{})
	if err != nil || waiter == nil {
		t.Fatal("missing exact incompatible waiter", waiter, err)
	}
	before := len(e.locks.Inspect(c.Transactions.ticket).Journals[c.Transactions.ticket])
	probe, err := e.locks.StartAcquire(context.Background(), c.Transactions.ticket, resource, lock.ForKeyShare, lock.Policy{NoWait: true})
	if probe != nil || !errors.Is(err, lock.ErrUnavailable) {
		t.Fatal("compatible newcomer barged past earlier UPDATE", probe, err)
	}
	self, err := e.locks.StartAcquire(context.Background(), a.Transactions.ticket, resource, lock.ForShare, lock.Policy{NoWait: true})
	if self != nil || err != nil {
		t.Fatal("self upgrade joined its own blocked queue", self, err)
	}
	lockState := e.locks.Inspect(c.Transactions.ticket)
	entry := lockState.Resources[resource]
	bad := len(entry.Pending) != 1 || entry.Pending[0].Request != waiter || entry.Holders[a.Transactions.ticket].Mode != (lock.ForKeyShare|lock.ForShare) || len(lockState.Journals[c.Transactions.ticket]) != before
	if bad {
		t.Fatal("conditional probe altered queue/journal/self contributions")
	}
	cancel()
	if err := waiter.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	probe, err = e.locks.StartAcquire(context.Background(), c.Transactions.ticket, resource, lock.ForKeyShare, lock.Policy{NoWait: true})
	if probe != nil || err != nil {
		t.Fatal("cancelled predecessor left ghost conditional denial", probe, err)
	}
}
