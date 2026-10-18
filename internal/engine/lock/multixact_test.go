package lock

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// multiTuple is a row version of one fixed relation.
func (f *lockFixture) multiTuple(row ids.RowID) MultiTuple {
	resource := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999}), Row: row}
	return MultiTuple{resource, ids.VersionID(row)}
}

func (f *lockFixture) multiGrant(owner *Ticket, tuple MultiTuple, mode Mode) {
	f.t.Helper()
	if err := f.result(f.request(owner, tuple.Resource, mode)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *lockFixture) multiEmit(owner *Ticket, tuple MultiTuple, mode MultiMode) {
	f.t.Helper()
	if err := f.m.PhysicalEvents(owner, []MultiEvent{{Tuple: tuple, Mode: mode}}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *lockFixture) multiWant(id uint32, want ...string) {
	f.t.Helper()
	members, err := f.m.MultiXactMembers(nil, id)
	if err != nil {
		f.t.Fatal(err)
	}
	got := make([]string, len(members))
	for i, m := range members {
		got[i] = fmt.Sprintf("%d/%s", m.XID, m.Mode.Name())
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		f.t.Fatalf("MultiXact %d: %v want %v", id, got, want)
	}
}

func TestMultiXactAgeArithmetic(t *testing.T) {
	for _, tc := range []struct {
		next, xid uint32
		want      int32
	}{{1, 0, 2147483647}, {1, 1, 0}, {1, 2, -1}, {1, 4294967295, 2}, {2147483649, 1, -2147483648}} {
		if got := multiTransactionAge(tc.next, tc.xid); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestMultiXactCacheLRU(t *testing.T) {
	var cache []uint32
	for id := uint32(1); id <= 257; id++ {
		cache = rememberMultiXact(cache, id)
	}
	if len(cache) != 256 || cache[0] != 257 || cache[255] != 2 {
		t.Fatal("cache cap/oldest eviction", len(cache), cache[0], cache[len(cache)-1])
	}
	cache = rememberMultiXact(cache, 2)
	cache = rememberMultiXact(cache, 258)
	if len(cache) != 256 || cache[0] != 258 || cache[1] != 2 || cache[255] != 4 {
		t.Fatal("cache hit did not refresh LRU")
	}
	if got := testing.AllocsPerRun(100, func() { cache = rememberMultiXact(cache, 2) }); got != 0 {
		t.Fatal("cache reentry allocated", got)
	}
}

func TestMultiXactCounterWrapCacheAndRetirement(t *testing.T) {
	f := newLockFixture(t)
	a, b := f.tx(), f.tx()
	a.MirrorFullXID(3)
	b.MirrorFullXID(4)
	f.m.mu.Lock()
	f.m.multiNext = 4294967295
	f.m.mu.Unlock()
	first := f.multiTuple(1)
	second := f.multiTuple(2)
	for _, tuple := range []MultiTuple{first, second} {
		f.multiGrant(a, tuple, ForKeyShare)
		f.multiEmit(a, tuple, MultiKeyShare)
		f.multiGrant(b, tuple, ForShare)
		f.multiEmit(b, tuple, MultiShare)
	}
	if f.m.NextMultiXact() != 1 {
		t.Fatal("zero skip/owner-local same-bag cache")
	}
	f.multiWant(4294967295, "3/keysh", "4/sh")
	if f.m.multiRows[first].id != 4294967295 || f.m.multiRows[second].id != 4294967295 {
		t.Fatal("same backend did not reuse exact member set")
	}
	f.m.mu.Lock()
	f.m.retireMultiXactsLocked()
	f.m.mu.Unlock()
	if f.m.NextMultiXact() != 1 {
		t.Fatal("retirement reset counter")
	}
	for _, tc := range []struct {
		id      uint32
		message string
	}{{0, "invalid MultiXactId: 0"}, {1, "MultiXactId 1 has not been created yet -- apparent wraparound"}, {4294967295, "MultiXactId 4294967295 does no longer exist -- apparent wraparound"}} {
		_, err := f.m.MultiXactMembers(nil, tc.id)
		var state *sqlstate.Error
		code := "XX000"
		if tc.id == 0 {
			code = "22023"
		}
		if !errors.As(err, &state) || state.Code != code || state.Message != tc.message {
			t.Fatal("retention validation", err)
		}
	}
}
