package lock

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	"stuntgres/internal/engine/ids"
)

func TestPgsurgeryFreezeTupleResource(t *testing.T) {
	f := newLockFixture(t)
	ref := f.relation().ObjectRef
	ordinary, err := TupleResource(ref, 7, 0, 0)
	if err != nil || ordinary != (Resource{ObjectRef: ref, Row: 7}) {
		t.Fatal("ordinary tuple key changed", ordinary, err)
	}
	repaired, err := TupleResource(ref, 7, 11, 1<<40|9)
	if err != nil || repaired.Kind != RepairedTupleResource || repaired.Row != 11 || repaired.High != 1<<8 || repaired.Low != 9 {
		t.Fatal("repair anchor not encoded inline", repaired, err)
	}
	again, err := TupleResource(ref, 7, 11, 1<<40|10)
	if err != nil || again == repaired || again == ordinary {
		t.Fatal("re-repair did not distinguish exact target", again, err)
	}
	sibling, err := TupleResource(ref, 7, 12, 1<<40|9)
	if err != nil || sibling == repaired {
		t.Fatal("same-RowID anchors collapsed", err)
	}
	for _, bad := range []struct {
		row    ids.RowID
		anchor ids.VersionID
		stamp  uint64
	}{
		{0, 0, 0}, {7, 11, 0}, {7, 0, 1},
	} {
		if _, err := TupleResource(ref, bad.row, bad.anchor, bad.stamp); err == nil {
			t.Fatal("invalid anchor pair accepted", bad)
		}
	}
	if _, err := TupleResource(ids.ObjectRef{}, 7, 11, 1); err == nil {
		t.Fatal("unproven relation accepted")
	}
	badRef := ref
	badRef.Address.ClassID = ids.TypeClassID
	if _, err := TupleResource(badRef, 7, 11, 1); err == nil {
		t.Fatal("nonrelation accepted")
	}
	if unsafe.Sizeof(Resource{}) != 128 {
		t.Fatal("Resource compact layout changed", unsafe.Sizeof(Resource{}))
	}
	owner := f.tx()
	if f.request(owner, ordinary, ForUpdate) != nil {
		t.Fatal("ordinary grant queued")
	}
	if got := testing.AllocsPerRun(100, func() {
		key, err := TupleResource(ref, 7, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		request, err := f.m.StartAcquire(context.Background(), owner, key, ForUpdate, Policy{})
		if err != nil || request != nil {
			t.Fatal("ordinary reentry failed", err)
		}
	}); got != 0 {
		t.Fatal("ordinary tuple construction/acquisition allocated", got)
	}
	for _, key := range []Resource{repaired, again, sibling} {
		if f.request(owner, key, ForUpdate) != nil {
			t.Fatal("independent repaired key queued")
		}
	}
	for _, bad := range []Resource{
		{ObjectRef: ref, Kind: RepairedTupleResource, Row: 11},
		{ObjectRef: ref, Kind: RepairedTupleResource, Row: 11, Low: 1, RoleTuple: true},
		{ObjectRef: ref, Kind: RepairedTupleResource, Low: 1},
	} {
		if _, err := f.m.StartAcquire(context.Background(), owner, bad, ForUpdate, Policy{}); err == nil {
			t.Fatal("malformed repaired resource accepted", bad)
		}
	}
	if _, err := f.m.StartAcquire(context.Background(), owner, repaired, AccessExclusive, Policy{}); err == nil {
		t.Fatal("repair anchor admitted as relation lock")
	}
	if f.m.MissingRowGrant(owner, ref, []Resource{repaired}) != -1 {
		t.Fatal("real repaired row grant missed")
	}
	other := f.relation().ObjectRef
	if f.m.MissingRowGrant(owner, other, []Resource{repaired}) != 0 {
		t.Fatal("wrong relation row grant accepted")
	}
}

func TestPgsurgeryFreezeTupleHistory(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	observer, old, fresh, waiter, keyOwner := f.tx(), f.tx(), f.tx(), f.tx(), f.tx()
	old.MirrorFullXID(30)
	fresh.MirrorFullXID(40)
	keyOwner.MirrorFullXID(50)
	ref := f.relation().ObjectRef
	makeTuple := func(anchor ids.VersionID, stamp uint64) MultiTuple {
		t.Helper()
		resource, err := TupleResource(ref, 7, anchor, stamp)
		if err != nil {
			t.Fatal(err)
		}
		return MultiTuple{Resource: resource, Version: 11}
	}
	original, repaired, again := makeTuple(0, 0), makeTuple(11, 1), makeTuple(11, 2)
	f.multiGrant(old, original, ForUpdate)
	f.multiEmit(old, original, MultiUpdateLock)
	pending := f.request(waiter, original.Resource, ForUpdate)
	if pending == nil {
		t.Fatal("old ticket waiter did not queue")
	}
	f.multiGrant(fresh, repaired, ForUpdate)
	f.multiEmit(fresh, repaired, MultiUpdateLock)
	if f.m.MissingRowGrant(old, ref, []Resource{repaired.Resource}) != 0 {
		t.Fatal("old ordinary row grant covered fresh repair anchor")
	}
	capture := func(tuple MultiTuple) TupleMaintenance {
		t.Helper()
		facts := []TupleMaintenance{{Tuple: tuple}}
		before := f.m.Inspect(old, fresh, waiter, keyOwner, observer)
		if err := f.m.CaptureTupleMaintenance(ctx, observer, facts); err != nil {
			t.Fatal(err)
		}
		if after := f.m.Inspect(old, fresh, waiter, keyOwner, observer); !reflect.DeepEqual(before, after) {
			t.Fatal("raw capture changed manager state")
		}
		return facts[0]
	}
	oldCapture, newCapture := capture(original), capture(repaired)
	if !oldCapture.Present || !oldCapture.Active || oldCapture.NewestFullXID != 30 || oldCapture.Xmax != 30 || oldCapture.Multi || !newCapture.Present || !newCapture.Active || newCapture.NewestFullXID != 40 || newCapture.Xmax != 40 || newCapture.Multi || capture(again).Present {
		t.Fatal("exact target raw keys collapsed", oldCapture, newCapture)
	}
	if pending.Terminal() || !f.m.Holds(old, original.Resource, ForUpdate) {
		t.Fatal("new anchor discarded old ownership/waiter")
	}
	fresh.MirrorCompletion(ids.TxAborted)
	f.m.End(fresh)
	endedCapture := newCapture
	endedCapture.Active = false
	if got := capture(repaired); got != endedCapture {
		t.Fatal("ended/aborted raw xmax erased", got, newCapture)
	}
	rows, err := f.m.Pgrowlocks(ctx, observer, []PgrowlocksTuple{{Tuple: repaired}})
	if err != nil || len(rows) != 0 {
		t.Fatal("aborted raw header falsely eligible", rows, err)
	}
	f.multiGrant(keyOwner, again, ForKeyShare)
	f.multiEmit(keyOwner, again, MultiKeyShare)
	updater := f.tx()
	updater.MirrorFullXID(60)
	f.multiGrant(updater, again, ForNoKeyUpdate)
	if err := f.m.PhysicalEvents(updater, []MultiEvent{{Tuple: again, Successor: 12, Mode: MultiNoKeyAction}}); err != nil {
		t.Fatal(err)
	}
	successor := again
	successor.Version = 12
	inheritedCapture := capture(successor)
	if !inheritedCapture.Present || inheritedCapture.Xmax != 50 || inheritedCapture.Multi {
		t.Fatal("successor did not inherit original singleton", inheritedCapture)
	}
	multiCapture := capture(again)
	if !multiCapture.Multi || multiCapture.Xmax == 0 {
		t.Fatal("real updating MultiXact missing", multiCapture)
	}
	rows, err = f.m.Pgrowlocks(ctx, observer, []PgrowlocksTuple{{Tuple: again}, {Tuple: successor}})
	if err != nil || len(rows) != 2 || rows[0].Locker != multiCapture.Xmax || !rows[0].Multi || rows[1].Locker != 50 || rows[1].Multi || len(rows[1].Members) != 1 || rows[1].Members[0].Mode != MultiKeyShare {
		t.Fatal("inspector/raw capture diverged", rows, err)
	}
	updater.MirrorCompletion(ids.TxCommitted)
	f.m.End(updater)
	if got := capture(again); got != multiCapture {
		t.Fatal("committed raw MultiXact changed", got)
	}
	nextUpdater := f.tx()
	nextUpdater.MirrorFullXID(61)
	f.multiGrant(nextUpdater, successor, ForNoKeyUpdate)
	if err := f.m.PhysicalEvents(nextUpdater, []MultiEvent{{Tuple: successor, Successor: 13, Mode: MultiNoKeyAction}}); err != nil {
		t.Fatal(err)
	}
	// This second UPDATE changes successor12's own raw header to a real
	// MultiXact (original key locker plus updater61). New13, not old12,
	// inherits the original singleton. Owner end must restore neither header.
	promotedCapture := capture(successor)
	if !promotedCapture.Present || !promotedCapture.Multi || promotedCapture.Xmax == 0 || promotedCapture.Xmax == multiCapture.Xmax {
		t.Fatal("second UPDATE did not promote its actual predecessor header", promotedCapture)
	}
	members, err := f.m.MultiXactMembers(f.domain, nil, promotedCapture.Xmax)
	if err != nil || len(members) != 2 || members[0].XID != 50 || members[0].Mode != MultiKeyShare ||
		members[1].XID != 61 || members[1].Mode != MultiNoKeyAction {
		t.Fatal("promoted raw header lacks original locker and real second updater", members, err)
	}
	later := successor
	later.Version = 13
	laterCapture := capture(later)
	if !laterCapture.Present || laterCapture.Xmax != 50 || laterCapture.Multi {
		t.Fatal("later successor lost inherited owner", laterCapture)
	}
	if !f.m.Holds(keyOwner, later.Resource, ForKeyShare) {
		t.Fatal("later successor invented a new arbitration key")
	}
	nextUpdater.MirrorCompletion(ids.TxCommitted)
	f.m.End(nextUpdater)
	keyOwner.MirrorCompletion(ids.TxAborted)
	f.m.End(keyOwner)
	endedPromoted := promotedCapture
	endedPromoted.Active = false
	if got := capture(successor); got != endedPromoted {
		t.Fatal("owner end changed promoted predecessor raw header", got, promotedCapture)
	}
	endedLater := laterCapture
	endedLater.Active = false
	if got := capture(later); got != endedLater {
		t.Fatal("owner end changed final inherited singleton", got, laterCapture)
	}
	if inheritedCapture.Xmax != 50 || inheritedCapture.Multi {
		t.Fatal("second UPDATE changed prior detached singleton capture", inheritedCapture)
	}
	rows, err = f.m.Pgrowlocks(ctx, observer, []PgrowlocksTuple{{Tuple: successor}, {Tuple: later}})
	if err != nil || len(rows) != 0 {
		t.Fatal("ended raw predecessor or successor remained inspector-eligible", rows, err)
	}
	if oldCapture.Xmax != 30 || newCapture.Xmax != 40 || pending.Terminal() {
		t.Fatal("later captures changed old snapshot or settled unrelated waiter")
	}
	f.m.End(old)
	if err := f.result(pending); err != nil {
		t.Fatal("old waiter did not settle through genuine owner end", err)
	}
}

func TestPgsurgeryFreezeTuplePretriggerInheritance(t *testing.T) {
	for _, repaired := range []bool{false, true} {
		name := "ordinary"
		if repaired {
			name = "repaired"
		}
		t.Run(name, func(t *testing.T) {
			for _, prelock := range []Mode{0, ForNoKeyUpdate, ForUpdate} {
				label := "action_only"
				if prelock == ForNoKeyUpdate {
					label = "no_key_prelock"
				}
				if prelock == ForUpdate {
					label = "update_prelock"
				}
				t.Run(label, func(t *testing.T) {
					f := newLockFixture(t)
					ctx := context.Background()
					owner, observer := f.tx(), f.tx()
					owner.MirrorFullXID(40)
					ref := f.relation().ObjectRef
					var anchor ids.VersionID
					var stamp uint64
					if repaired {
						anchor, stamp = 11, 7
					}
					resource, err := TupleResource(ref, 7, anchor, stamp)
					if err != nil {
						t.Fatal(err)
					}
					tuple := MultiTuple{Resource: resource, Version: 11}
					f.multiGrant(owner, tuple, ForNoKeyUpdate)
					capture := func(tuple MultiTuple) TupleMaintenance {
						t.Helper()
						facts := []TupleMaintenance{{Tuple: tuple}}
						if err := f.m.CaptureTupleMaintenance(ctx, observer, facts); err != nil {
							t.Fatal(err)
						}
						return facts[0]
					}
					if prelock != 0 {
						f.multiGrant(owner, tuple, prelock)
						mode, err := MultiLockMode(prelock)
						if err != nil {
							t.Fatal(err)
						}
						f.multiEmit(owner, tuple, mode)
					}
					beforeAction := capture(tuple)
					// Exercise repeated manager action events independently of the
					// producer, which publishes only its actually accepted action.
					f.multiEmit(owner, tuple, MultiNoKeyAction)
					rows, err := f.m.Pgrowlocks(ctx, owner, []PgrowlocksTuple{{Tuple: tuple}})
					if err != nil || len(rows) != 0 {
						t.Fatal("own action did not reach eligibility filter", rows, err)
					}
					f.multiEmit(owner, tuple, MultiNoKeyAction)
					if err := f.m.PhysicalEvents(owner, []MultiEvent{{Tuple: tuple, Successor: 12, Mode: MultiNoKeyAction}}); err != nil {
						t.Fatal(err)
					}
					successor := tuple
					successor.Version = 12
					raw := capture(successor)
					if prelock == 0 {
						if raw.Present || raw.Xmax != 0 || raw.Multi || beforeAction.Present {
							t.Fatal("action-only update fabricated inherited own lock", raw, beforeAction)
						}
						return
					}
					if !raw.Present || raw.Xmax != 40 || raw.Multi || raw.Tuple.Resource != resource ||
						!beforeAction.Present || beforeAction.Xmax != 40 || beforeAction.Multi {
						t.Fatal("genuine own prelock did not survive action/successor", raw, beforeAction)
					}
					rows, err = f.m.Pgrowlocks(ctx, observer, []PgrowlocksTuple{{Tuple: successor}})
					if err != nil || len(rows) != 1 || rows[0].Locker != 40 || rows[0].Multi ||
						len(rows[0].Members) != 1 || rows[0].Members[0].Mode != MultiKeyShare {
						t.Fatal("inherited singleton lost effective lock-only provenance", rows, err)
					}
					checkpoint := f.m.Checkpoint(owner)
					if checkpoint[resource].Multi[successor.Version] == 0 {
						t.Fatal("successor raw prelock lacks genuine contribution provenance")
					}
					owner.MirrorCompletion(ids.TxAborted)
					f.m.End(owner)
					ended := raw
					ended.Active = false
					if got := capture(successor); got != ended || beforeAction.Xmax != 40 {
						t.Fatal("owner abort changed detached or retained raw prelock", got, raw)
					}
					rows, err = f.m.Pgrowlocks(ctx, observer, []PgrowlocksTuple{{Tuple: successor}})
					if err != nil || len(rows) != 0 {
						t.Fatal("aborted inherited lock remained eligible", rows, err)
					}
				})
			}
		})
	}
}
