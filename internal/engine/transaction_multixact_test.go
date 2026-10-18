package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func identityMultiUnitTuple(e *Engine, row ids.RowID) multiTuple {
	resource := blockingUnitResource(e)
	resource.Row = row
	return multiTuple{resource, ids.VersionID(row)}
}
func identityMultiGrant(t *testing.T, e *Engine, owner *relationTicket, tuple multiTuple, mode relationLockMode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := e.locks.StartAcquire(ctx, owner, tuple.lockResource, mode, relationLockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if request != nil {
		if err := request.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}
func identityMultiEmit(t *testing.T, e *Engine, owner *relationTicket, tuple multiTuple, mode multiMode) {
	t.Helper()
	if err := e.locks.PhysicalEvents(owner, []multiEvent{{Tuple: tuple, Mode: mode}}); err != nil {
		t.Fatal(err)
	}
}
func identityMultiWant(t *testing.T, e *Engine, id uint32, want ...string) {
	t.Helper()
	members, err := e.locks.MultiXactMembers(nil, id)
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
		again, err := e.locks.MultiXactMembers(nil, id)
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
		identityMultiGrant(t, e, a, tuple, keyShare)
		identityMultiEmit(t, e, a, tuple, multiKeyShare)
		if e.locks.NextMultiXact() != 1 {
			t.Fatal("first locker fabricated multi")
		}
		identityMultiGrant(t, e, b, tuple, rowShare)
		identityMultiEmit(t, e, b, tuple, multiShare)
		identityMultiWant(t, e, 1, "3/keysh", "4/sh")
		identityMultiEmit(t, e, b, tuple, multiShare)
		if e.locks.NextMultiXact() != 2 {
			t.Fatal("exact-pair reentry allocated")
		}
		transactionEnd(t, bs, b.Ref(), false)
		identityMultiEmit(t, e, a, tuple, multiKeyShare)
		if e.locks.NextMultiXact() != 2 {
			t.Fatal("exact-pair reentry pruned finished member")
		}
		identityMultiWant(t, e, 1, "3/keysh", "4/sh")
		identityMultiGrant(t, e, c, tuple, rowShare)
		identityMultiEmit(t, e, c, tuple, multiShare)
		identityMultiWant(t, e, 2, "3/keysh", "5/sh")
		e.mu.Lock()
		e.endSerializableLocked(c.Ref(), false)
		e.mu.Unlock()
		e.endLockOwner(c)
		identityMultiGrant(t, e, a, tuple, rowUpdate)
		identityMultiEmit(t, e, a, tuple, multiUpdateLock)
		identityMultiWant(t, e, 3, "3/keysh", "3/forupd")
		identityMultiEmit(t, e, a, tuple, multiUpdateAction)
		identityMultiWant(t, e, 4, "3/keysh", "3/forupd", "3/upd")
		identityMultiWant(t, e, 1, "3/keysh", "4/sh")
		identityMultiWant(t, e, 3, "3/keysh", "3/forupd")
		e.mu.Lock()
		e.endSerializableLocked(a.Ref(), false)
		e.mu.Unlock()
		e.endLockOwner(a)
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
			identityMultiGrant(t, e, a, tuple, keyShare)
			identityMultiEmit(t, e, a, tuple, multiKeyShare)
			identityMultiGrant(t, e, b, tuple, noKeyUpdate)
			identityMultiEmit(t, e, b, tuple, multiNoKeyAction)
			identityMultiWant(t, e, 1, "3/keysh", "4/nokeyupd")
			transactionEnd(t, bs, b.Ref(), committed)
			identityMultiGrant(t, e, c, tuple, rowShare)
			identityMultiEmit(t, e, c, tuple, multiShare)
			if committed {
				identityMultiWant(t, e, 2, "3/keysh", "4/nokeyupd", "5/sh")
			} else {
				identityMultiWant(t, e, 2, "3/keysh", "5/sh")
			}
			identityMultiWant(t, e, 1, "3/keysh", "4/nokeyupd")
			if committed {
				identityMultiGrant(t, e, c, tuple, noKeyUpdate)
				before := e.locks.NextMultiXact()
				if err := e.locks.PhysicalEvents(c, []multiEvent{{Tuple: tuple, Mode: multiNoKeyAction}}); err == nil || e.locks.NextMultiXact() != before {
					t.Fatal("two updaters admitted or partially published", err)
				}
				identityMultiWant(t, e, 2, "3/keysh", "4/nokeyupd", "5/sh")
			}
		})
	}
	t.Run("counter_wrap_cache_and_retirement", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		e.locks.mu.Lock()
		e.locks.multiNext = 4294967295
		e.locks.mu.Unlock()
		first := identityMultiUnitTuple(e, 1)
		second := identityMultiUnitTuple(e, 2)
		for _, tuple := range []multiTuple{first, second} {
			identityMultiGrant(t, e, a, tuple, keyShare)
			identityMultiEmit(t, e, a, tuple, multiKeyShare)
			identityMultiGrant(t, e, b, tuple, rowShare)
			identityMultiEmit(t, e, b, tuple, multiShare)
		}
		if e.locks.NextMultiXact() != 1 {
			t.Fatal("zero skip/owner-local same-bag cache")
		}
		identityMultiWant(t, e, 4294967295, "3/keysh", "4/sh")
		if e.locks.multiRows[first].id != 4294967295 || e.locks.multiRows[second].id != 4294967295 {
			t.Fatal("same backend did not reuse exact member set")
		}
		e.locks.mu.Lock()
		e.locks.retireMultiXactsLocked()
		e.locks.mu.Unlock()
		if e.locks.NextMultiXact() != 1 {
			t.Fatal("retirement reset counter")
		}
		for _, tc := range []struct {
			id      uint32
			message string
		}{{0, "invalid MultiXactId: 0"}, {1, "MultiXactId 1 has not been created yet -- apparent wraparound"}, {4294967295, "MultiXactId 4294967295 does no longer exist -- apparent wraparound"}} {
			_, err := e.locks.MultiXactMembers(nil, tc.id)
			var state *sqlstate.Error
			code := "XX000"
			if tc.id == 0 {
				code = "22023"
			}
			if !errors.As(err, &state) || state.Code != code || state.Message != tc.message {
				t.Fatal("retention validation", err)
			}
		}
	})
}

func TestTransactionIdentityGrantActions(t *testing.T) {
	e := New()
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	pgLocksUnitAssign(t, e, a)
	pgLocksUnitAssign(t, e, b)
	first, second := identityMultiUnitTuple(e, 1), identityMultiUnitTuple(e, 2)
	identityMultiGrant(t, e, a, first, keyShare)
	identityMultiEmit(t, e, a, first, multiKeyShare)
	identityMultiGrant(t, e, b, first, noKeyUpdate)
	if e.locks.NextMultiXact() != 1 || len(e.locks.multiHistory) != 0 {
		t.Fatal("candidate grant fabricated a physical event")
	}
	identityMultiEmit(t, e, b, first, multiNoKeyLock)
	identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	identityMultiGrant(t, e, a, second, keyShare)
	identityMultiEmit(t, e, a, second, multiKeyShare)
	identityMultiGrant(t, e, b, second, noKeyUpdate)
	identityMultiEmit(t, e, b, second, multiNoKeyAction)
	identityMultiWant(t, e, 2, "3/keysh", "4/nokeyupd")
	_, waiter := blockingUnitOwner(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := e.locks.StartAcquire(ctx, waiter, first.lockResource, noKeyUpdate, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal("missing actual wait", request, err)
	}
	if waiter.AssignedPublicXID() != 0 || e.locks.NextMultiXact() != 3 {
		t.Fatal("queued request assigned/published membership")
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel actual wait", err)
	}
	if e.locks.NextMultiXact() != 3 || waiter.AssignedPublicXID() != 0 {
		t.Fatal("canceled request published membership")
	}
	third := identityMultiUnitTuple(e, 3)
	identityMultiGrant(t, e, a, third, keyShare)
	identityMultiEmit(t, e, a, third, multiKeyShare)
	identityMultiGrant(t, e, b, third, rowShare)
	before := e.locks.NextMultiXact()
	bad := third
	bad.Version = 0
	if err := e.locks.PhysicalEvents(b, []multiEvent{{Tuple: third, Mode: multiShare}, {Tuple: bad, Mode: multiShare}}); err == nil || e.locks.NextMultiXact() != before || e.locks.multiRows[third].id != 0 {
		t.Fatal("batch preflight published a prefix", err)
	}
	for _, change := range []func(*multiTuple){func(k *multiTuple) { k.Row = 0 }, func(k *multiTuple) { k.Kind = advisoryBigintResource }, func(k *multiTuple) { k.Epoch++ }, func(k *multiTuple) { k.ObjectRef = New().domain.Ref(k.ObjectRef.Epoch, k.ObjectRef.Address) }} {
		bad := third
		change(&bad)
		if err := e.locks.PhysicalEvents(b, []multiEvent{{Tuple: bad, Mode: multiShare}}); err == nil || e.locks.NextMultiXact() != before {
			t.Fatal("ineligible physical event", err)
		}
	}
	for mode, want := range []string{"keysh", "sh", "fornokeyupd", "forupd", "nokeyupd", "upd"} {
		if multiMode(mode).Name() != want {
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
	identityMultiGrant(t, e, a, first, keyShare)
	identityMultiEmit(t, e, a, first, multiKeyShare)
	identityMultiGrant(t, e, b, first, noKeyUpdate)
	identityMultiEmit(t, e, b, first, multiNoKeyLock)
	checkpoint := e.locks.Checkpoint(b)
	identityMultiGrant(t, e, a, second, keyShare)
	identityMultiEmit(t, e, a, second, multiKeyShare)
	identityMultiGrant(t, e, b, second, noKeyUpdate)
	identityMultiEmit(t, e, b, second, multiNoKeyLock)
	// Same set is cached by the actual expanding backend, independently of tuple.
	if e.locks.NextMultiXact() != 2 {
		t.Fatal("owner-local cache allocated again")
	}
	e.locks.Restore(b, checkpoint)
	if b.AssignedPublicXID() != 4 || b.multiContributions[first] != (1<<multiNoKeyLock) || b.multiContributions[second] != 0 {
		t.Fatal("savepoint lost identity or restored provenance from grant bits")
	}
	identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	identityMultiGrant(t, e, c, second, rowShare)
	identityMultiEmit(t, e, c, second, multiShare)
	identityMultiWant(t, e, 2, "3/keysh", "5/sh")
	identityMultiWant(t, e, 1, "3/keysh", "4/fornokeyupd")
	third := identityMultiUnitTuple(e, 3)
	identityMultiGrant(t, e, a, third, keyShare)
	identityMultiEmit(t, e, a, third, multiKeyShare)
	original := e.locks.Checkpoint(a)
	identityMultiGrant(t, e, a, third, rowUpdate)
	identityMultiEmit(t, e, a, third, multiUpdateLock)
	e.locks.Restore(a, original)
	if a.multiContributions[third] != (1<<multiKeyShare) || len(e.locks.multiRows[third].members) != 1 || e.locks.multiRows[third].members[0].Mode != multiKeyShare {
		t.Fatal("singleton upgrade rollback lost captured physical provenance")
	}
}
