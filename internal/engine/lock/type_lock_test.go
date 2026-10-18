package lock

import (
	"context"
	"errors"
	"testing"
)

// Type locks (enum and domain DDL) admit only TypeExclusive on the type's
// relation-free resource, and keep transaction and savepoint lifetimes.
func TestTypeLockLifecycle(t *testing.T) {
	f := newLockFixture(t)
	resource := f.typeResource()
	ref := resource.ObjectRef
	at, bt := f.tx(), f.tx()
	for _, bad := range []struct {
		r Resource
		m Mode
	}{
		{resource, AccessExclusive},
		{Resource{ObjectRef: ref, Row: 1}, TypeExclusive},
		{Resource{ObjectRef: ref, key: &UniqueKey{}}, TypeExclusive},
		{Resource{ObjectRef: ref, High: 1}, TypeExclusive},
	} {
		if _, err := f.m.StartAcquire(context.Background(), at, bad.r, bad.m, Policy{}); err == nil {
			t.Fatal("invalid type lock admitted", bad)
		}
	}
	mark := f.m.Checkpoint(at)
	if err := f.m.Acquire(context.Background(), at, resource, TypeExclusive); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Acquire(context.Background(), at, resource, TypeExclusive); err != nil {
		t.Fatal("same ticket reentry", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := f.m.StartAcquire(ctx, bt, resource, TypeExclusive, Policy{})
	if err != nil || request == nil {
		t.Fatal("same-type request did not wait", err)
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	request, err = f.m.StartAcquire(context.Background(), bt, resource, TypeExclusive, Policy{})
	if err != nil || request == nil {
		t.Fatal("cancel released holder", err)
	}
	// A savepoint rollback drops exactly the new contribution and wakes waiter.
	f.m.Restore(at, mark)
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	if !Conflicts(TypeExclusive, TypeExclusive) || Conflicts(TypeExclusive, AccessShare) || deadlockMode(TypeExclusive) != "ExclusiveLock" {
		t.Fatal("type lock family semantics")
	}
}
