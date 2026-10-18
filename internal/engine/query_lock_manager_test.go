package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryLockConditionalRowManager(t *testing.T) {
	modes := []relationLockMode{keyShare, rowShare, noKeyUpdate, rowUpdate}
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
				row := e.storage[ref.Address].versions[0].row
				e.mu.Unlock()
				resource := lockResource{ObjectRef: ref, Row: row}
				if err := e.locks.Acquire(context.Background(), a.transactions.ticket, resource, held); err != nil {
					t.Fatal(err)
				}
				before := len(b.transactions.ticket.resources)
				request, err := e.locks.StartAcquire(context.Background(), b.transactions.ticket, resource, requested, relationLockPolicy{NoWait: true, Timeout: time.Hour})
				conflict := lockConflict(requested, held)
				if request != nil || conflict && !errors.Is(err, errRelationUnavailable) || !conflict && err != nil {
					t.Fatal(request, err, conflict)
				}
				e.locks.mu.Lock()
				entry := e.locks.entries[resource]
				bad := len(entry.pending) != 0 || entry.holders[a.transactions.ticket].Mode != held
				if conflict {
					bad = bad || entry.holders[b.transactions.ticket].Mode != 0 || len(b.transactions.ticket.resources) != before
				} else {
					bad = bad || entry.holders[b.transactions.ticket].Mode != requested
				}
				e.locks.mu.Unlock()
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
	row := e.storage[ref.Address].versions[0].row
	e.mu.Unlock()
	resource := lockResource{ObjectRef: ref, Row: row}
	if err := e.locks.Acquire(context.Background(), a.transactions.ticket, resource, keyShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waiter, err := e.locks.StartAcquire(ctx, b.transactions.ticket, resource, rowUpdate, relationLockPolicy{})
	if err != nil || waiter == nil {
		t.Fatal("missing exact incompatible waiter", waiter, err)
	}
	before := len(c.transactions.ticket.resources)
	probe, err := e.locks.StartAcquire(context.Background(), c.transactions.ticket, resource, keyShare, relationLockPolicy{NoWait: true})
	if probe != nil || !errors.Is(err, errRelationUnavailable) {
		t.Fatal("compatible newcomer barged past earlier UPDATE", probe, err)
	}
	self, err := e.locks.StartAcquire(context.Background(), a.transactions.ticket, resource, rowShare, relationLockPolicy{NoWait: true})
	if self != nil || err != nil {
		t.Fatal("self upgrade joined its own blocked queue", self, err)
	}
	e.locks.mu.Lock()
	entry := e.locks.entries[resource]
	bad := len(entry.pending) != 1 || entry.pending[0] != waiter || entry.holders[a.transactions.ticket].Mode != (keyShare|rowShare) || len(c.transactions.ticket.resources) != before
	e.locks.mu.Unlock()
	if bad {
		t.Fatal("conditional probe altered queue/journal/self contributions")
	}
	cancel()
	if err := waiter.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	probe, err = e.locks.StartAcquire(context.Background(), c.transactions.ticket, resource, keyShare, relationLockPolicy{NoWait: true})
	if probe != nil || err != nil {
		t.Fatal("cancelled predecessor left ghost conditional denial", probe, err)
	}
}
