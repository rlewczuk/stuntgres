package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"testing"
	"time"
)

func identityMultiUnitTuple(e *Engine, row ids.RowID) lock.MultiTuple {
	resource := blockingUnitResource(e)
	resource.Row = row
	return lock.MultiTuple{resource, ids.VersionID(row)}
}
func identityMultiGrant(t *testing.T, e *Engine, owner *lock.Ticket, tuple lock.MultiTuple, mode lock.Mode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := e.Locks.StartAcquire(ctx, owner, tuple.Resource, mode, lock.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if request != nil {
		if err := request.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}
func identityMultiEmit(t *testing.T, e *Engine, owner *lock.Ticket, tuple lock.MultiTuple, mode lock.MultiMode) {
	t.Helper()
	if err := e.Locks.PhysicalEvents(owner, []lock.MultiEvent{{Tuple: tuple, Mode: mode}}); err != nil {
		t.Fatal(err)
	}
}
func identityMultiWant(t *testing.T, e *Engine, id uint32, want ...string) {
	t.Helper()
	members, err := e.Locks.MultiXactMembers(e.Catalog.Domain, nil, id)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(members))
	for i, m := range members {
		got[i] = fmt.Sprintf("%d/%s", m.XID, m.Mode.Name())
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MultiXact %d: %v want %v", id, got, want)
	}
	if len(members) != 0 {
		members[0].XID = 0
		again, err := e.Locks.MultiXactMembers(e.Catalog.Domain, nil, id)
		if err != nil || again[0].XID == 0 {
			t.Fatal("borrowed mutable history", err)
		}
	}
}

func TestTransactionIdentityMultiHistory(t *testing.T) {
	identityMultiSuccessorUnits(t)
	identityMultiCacheUnits(t)
	t.Run("immutable_same_owner_expansion", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		bs, b := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		pgLocksUnitAssign(t, e, c)
		tuple := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, tuple, lock.ForKeyShare)
		identityMultiEmit(t, e, a, tuple, lock.MultiKeyShare)
		if e.Locks.NextMultiXact(e.Catalog.Domain) != 1 {
			t.Fatal("first locker fabricated multi")
		}
		identityMultiGrant(t, e, b, tuple, lock.ForShare)
		identityMultiEmit(t, e, b, tuple, lock.MultiShare)
		identityMultiWant(t, e, 1, "3/keysh", "4/sh")
		identityMultiEmit(t, e, b, tuple, lock.MultiShare)
		if e.Locks.NextMultiXact(e.Catalog.Domain) != 2 {
			t.Fatal("exact-pair reentry allocated")
		}
		transactionEnd(t, bs, b.Ref(), false)
		identityMultiEmit(t, e, a, tuple, lock.MultiKeyShare)
		if e.Locks.NextMultiXact(e.Catalog.Domain) != 2 {
			t.Fatal("exact-pair reentry pruned finished member")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/sh")
		identityMultiGrant(t, e, c, tuple, lock.ForShare)
		identityMultiEmit(t, e, c, tuple, lock.MultiShare)
		identityMultiWant(t, e, 2, "3/keysh", "5/sh")
		e.Mu.Lock()
		e.endSerializableLocked(c.Ref(), false)
		e.Mu.Unlock()
		e.EndLockOwner(c)
		identityMultiGrant(t, e, a, tuple, lock.ForUpdate)
		identityMultiEmit(t, e, a, tuple, lock.MultiUpdateLock)
		identityMultiWant(t, e, 3, "3/keysh", "3/forupd")
		identityMultiEmit(t, e, a, tuple, lock.MultiUpdateAction)
		identityMultiWant(t, e, 4, "3/keysh", "3/forupd", "3/upd")
		identityMultiWant(t, e, 1, "3/keysh", "4/sh")
		identityMultiWant(t, e, 3, "3/keysh", "3/forupd")
		e.Mu.Lock()
		e.endSerializableLocked(a.Ref(), false)
		e.Mu.Unlock()
		e.EndLockOwner(a)
		identityMultiWant(t, e, 4, "3/keysh", "3/forupd", "3/upd")
	})
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("updater_committed_%v", committed), func(t *testing.T) {
			e := New()
			_, a := blockingUnitOwner(t, e)
			bs, b := blockingUnitOwner(t, e)
			_, c := blockingUnitOwner(t, e)
			pgLocksUnitAssign(t, e, a)
			pgLocksUnitAssign(t, e, b)
			pgLocksUnitAssign(t, e, c)
			tuple := identityMultiUnitTuple(e, 1)
			identityMultiGrant(t, e, a, tuple, lock.ForKeyShare)
			identityMultiEmit(t, e, a, tuple, lock.MultiKeyShare)
			identityMultiGrant(t, e, b, tuple, lock.ForNoKeyUpdate)
			identityMultiEmit(t, e, b, tuple, lock.MultiNoKeyAction)
			identityMultiWant(t, e, 1, "3/keysh", "4/nokeyupd")
			transactionEnd(t, bs, b.Ref(), committed)
			identityMultiGrant(t, e, c, tuple, lock.ForShare)
			identityMultiEmit(t, e, c, tuple, lock.MultiShare)
			if committed {
				identityMultiWant(t, e, 2, "3/keysh", "4/nokeyupd", "5/sh")
			} else {
				identityMultiWant(t, e, 2, "3/keysh", "5/sh")
			}
			identityMultiWant(t, e, 1, "3/keysh", "4/nokeyupd")
			if committed {
				identityMultiGrant(t, e, c, tuple, lock.ForNoKeyUpdate)
				before := e.Locks.NextMultiXact(e.Catalog.Domain)
				if err := e.Locks.PhysicalEvents(c, []lock.MultiEvent{{Tuple: tuple, Mode: lock.MultiNoKeyAction}}); err == nil || e.Locks.NextMultiXact(e.Catalog.Domain) != before {
					t.Fatal("two updaters admitted or partially published", err)
				}
				identityMultiWant(t, e, 2, "3/keysh", "4/nokeyupd", "5/sh")
			}
		})
	}
}

func TestTransactionIdentityGrantActions(t *testing.T) {
	e := New()
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	pgLocksUnitAssign(t, e, a)
	pgLocksUnitAssign(t, e, b)
	first, second := identityMultiUnitTuple(e, 1), identityMultiUnitTuple(e, 2)
	identityMultiGrant(t, e, a, first, lock.ForKeyShare)
	identityMultiEmit(t, e, a, first, lock.MultiKeyShare)
	identityMultiGrant(t, e, b, first, lock.ForNoKeyUpdate)
	if e.Locks.NextMultiXact(e.Catalog.Domain) != 1 || e.Locks.Inspect().MultiHistory[e.Catalog.Domain] != 0 {
		t.Fatal("candidate grant fabricated a physical event")
	}
	identityMultiEmit(t, e, b, first, lock.MultiNoKeyLock)
	identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	identityMultiGrant(t, e, a, second, lock.ForKeyShare)
	identityMultiEmit(t, e, a, second, lock.MultiKeyShare)
	identityMultiGrant(t, e, b, second, lock.ForNoKeyUpdate)
	identityMultiEmit(t, e, b, second, lock.MultiNoKeyAction)
	identityMultiWant(t, e, 2, "3/keysh", "4/nokeyupd")
	_, waiter := blockingUnitOwner(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := e.Locks.StartAcquire(ctx, waiter, first.Resource, lock.ForNoKeyUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal("missing actual wait", request, err)
	}
	if waiter.AssignedPublicXID() != 0 || e.Locks.NextMultiXact(e.Catalog.Domain) != 3 {
		t.Fatal("queued request assigned/published membership")
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel actual wait", err)
	}
	if e.Locks.NextMultiXact(e.Catalog.Domain) != 3 || waiter.AssignedPublicXID() != 0 {
		t.Fatal("canceled request published membership")
	}
	third := identityMultiUnitTuple(e, 3)
	identityMultiGrant(t, e, a, third, lock.ForKeyShare)
	identityMultiEmit(t, e, a, third, lock.MultiKeyShare)
	identityMultiGrant(t, e, b, third, lock.ForShare)
	before := e.Locks.NextMultiXact(e.Catalog.Domain)
	bad := third
	bad.Version = 0
	if err := e.Locks.PhysicalEvents(b, []lock.MultiEvent{{Tuple: third, Mode: lock.MultiShare}, {Tuple: bad, Mode: lock.MultiShare}}); err == nil || e.Locks.NextMultiXact(e.Catalog.Domain) != before || e.Locks.Inspect().MultiRows[third].ID != 0 {
		t.Fatal("batch preflight published a prefix", err)
	}
	for _, change := range []func(*lock.MultiTuple){func(k *lock.MultiTuple) { k.Row = 0 }, func(k *lock.MultiTuple) { k.Kind = lock.AdvisoryBigintResource }, func(k *lock.MultiTuple) { k.Epoch++ }, func(k *lock.MultiTuple) {
		k.ObjectRef = New().Catalog.Domain.Ref(k.ObjectRef.Epoch, k.ObjectRef.Address)
	}} {
		bad := third
		change(&bad)
		if err := e.Locks.PhysicalEvents(b, []lock.MultiEvent{{Tuple: bad, Mode: lock.MultiShare}}); err == nil || e.Locks.NextMultiXact(e.Catalog.Domain) != before {
			t.Fatal("ineligible physical event", err)
		}
	}
	for mode, want := range []string{"keysh", "sh", "fornokeyupd", "forupd", "nokeyupd", "upd"} {
		if lock.MultiMode(mode).Name() != want {
			t.Fatal("six physical status names", mode, want)
		}
	}
}

func TestTransactionIdentitySavepoint(t *testing.T) {
	e := New()
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	_, c := blockingUnitOwner(t, e)
	pgLocksUnitAssign(t, e, a)
	pgLocksUnitAssign(t, e, b)
	pgLocksUnitAssign(t, e, c)
	first, second := identityMultiUnitTuple(e, 1), identityMultiUnitTuple(e, 2)
	identityMultiGrant(t, e, a, first, lock.ForKeyShare)
	identityMultiEmit(t, e, a, first, lock.MultiKeyShare)
	identityMultiGrant(t, e, b, first, lock.ForNoKeyUpdate)
	identityMultiEmit(t, e, b, first, lock.MultiNoKeyLock)
	checkpoint := e.Locks.Checkpoint(b)
	identityMultiGrant(t, e, a, second, lock.ForKeyShare)
	identityMultiEmit(t, e, a, second, lock.MultiKeyShare)
	identityMultiGrant(t, e, b, second, lock.ForNoKeyUpdate)
	identityMultiEmit(t, e, b, second, lock.MultiNoKeyLock)
	// Same set is cached by the actual expanding backend, independently of tuple.
	if e.Locks.NextMultiXact(e.Catalog.Domain) != 2 {
		t.Fatal("owner-local cache allocated again")
	}
	e.Locks.Restore(b, checkpoint)
	if b.AssignedPublicXID() != 4 || e.Locks.Inspect(b).Contributions[b][first] != (1<<lock.MultiNoKeyLock) || e.Locks.Inspect(b).Contributions[b][second] != 0 {
		t.Fatal("savepoint lost identity or restored provenance from grant bits")
	}
	identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	identityMultiGrant(t, e, c, second, lock.ForShare)
	identityMultiEmit(t, e, c, second, lock.MultiShare)
	identityMultiWant(t, e, 2, "3/keysh", "5/sh")
	identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	third := identityMultiUnitTuple(e, 3)
	identityMultiGrant(t, e, a, third, lock.ForKeyShare)
	identityMultiEmit(t, e, a, third, lock.MultiKeyShare)
	original := e.Locks.Checkpoint(a)
	identityMultiGrant(t, e, a, third, lock.ForUpdate)
	identityMultiEmit(t, e, a, third, lock.MultiUpdateLock)
	e.Locks.Restore(a, original)
	if e.Locks.Inspect(a).Contributions[a][third] != (1<<lock.MultiKeyShare) || len(e.Locks.Inspect().MultiRows[third].Members) != 1 || e.Locks.Inspect().MultiRows[third].Members[0].Mode != lock.MultiKeyShare {
		t.Fatal("singleton upgrade rollback lost captured physical provenance")
	}
}
