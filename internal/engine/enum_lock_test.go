package engine

import (
	"context"
	"errors"
	"testing"
)

func TestEnumTypeLockLifecycle(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('old')"))
	done()
	mood := domainUnitType(t, e.CatalogRead(), "mood")
	ref := e.domain.Ref(e.state.epoch, mood.Address())
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	defer transactionEnd(t, a, ra, false)
	defer transactionEnd(t, b, rb, false)
	resource := lockResource{ObjectRef: ref}
	for _, bad := range []struct {
		r lockResource
		m relationLockMode
	}{
		{resource, accessExclusive},
		{lockResource{ObjectRef: ref, Row: 1}, typeExclusive},
		{lockResource{ObjectRef: ref, key: &uniqueLockKey{}}, typeExclusive},
		{lockResource{ObjectRef: ref, High: 1}, typeExclusive},
	} {
		if _, err := e.locks.StartAcquire(context.Background(), a.transactions.ticket, bad.r, bad.m, relationLockPolicy{}); err == nil {
			t.Fatal("invalid type lock admitted", bad)
		}
	}
	mark := e.locks.Checkpoint(a.transactions.ticket)
	if err := e.locks.Acquire(context.Background(), a.transactions.ticket, resource, typeExclusive); err != nil {
		t.Fatal(err)
	}
	if err := e.locks.Acquire(context.Background(), a.transactions.ticket, resource, typeExclusive); err != nil {
		t.Fatal("same ticket reentry", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := e.locks.StartAcquire(ctx, b.transactions.ticket, resource, typeExclusive, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal("same-type request did not wait", err)
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	request, err = e.locks.StartAcquire(context.Background(), b.transactions.ticket, resource, typeExclusive, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal("cancel released holder", err)
	}
	// A savepoint rollback drops exactly the new contribution and wakes waiter.
	e.locks.Restore(a.transactions.ticket, mark)
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	if !lockConflict(typeExclusive, typeExclusive) || lockConflict(typeExclusive, accessShare) || deadlockMode(typeExclusive) != "ExclusiveLock" {
		t.Fatal("type lock family semantics")
	}
}
