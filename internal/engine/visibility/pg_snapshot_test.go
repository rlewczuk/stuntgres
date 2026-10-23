package visibility

import (
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestSnapshotRegistryCapture(t *testing.T) {
	f := newFixture(t)
	a, b, observer := f.tx(), f.tx(), f.tx()
	initial, err := f.reg.CurrentSnapshotLocked(observer)
	if err != nil || initial.String() != "3:3:" {
		t.Fatal(initial, err)
	}
	if err := f.reg.AssignTransactionXIDLocked(a); err != nil {
		t.Fatal(err)
	}
	newest, err := f.reg.CurrentSnapshotLocked(observer)
	if err != nil || newest.String() != "3:3:" || newest.Visible(3) {
		t.Fatal(newest, err)
	}
	if err := f.reg.AssignTransactionXIDLocked(b); err != nil {
		t.Fatal(err)
	}
	f.reg.EndTransactionIdentityLocked(b, true)
	peer, err := f.reg.CurrentSnapshotLocked(observer)
	if err != nil || peer.String() != "3:5:3" || peer.Visible(3) {
		t.Fatal(peer, err)
	}
	own, err := f.reg.CurrentSnapshotLocked(a)
	if err != nil || own.String() != "3:5:" || !own.Visible(3) {
		t.Fatal(own, err)
	}
	// Snapshot advertisement is independent of prepared-owner liveness.
	entry := f.reg.Entries[a]
	entry.Snapshot = false
	f.reg.Entries[a] = entry
	detached, err := f.reg.CurrentSnapshotLocked(observer)
	if err != nil || detached.String() != "3:5:3" {
		t.Fatal(detached, err)
	}
	// End identity without removing the stale entry, as publication does.
	f.reg.EndTransactionIdentityLocked(a, false)
	completed, err := f.reg.CurrentSnapshotLocked(observer)
	if err != nil || completed.String() != "5:5:" || !completed.Visible(3) {
		t.Fatal(completed, err)
	}
	if peer.Visible(3) {
		t.Fatal("captured value changed after completion")
	}
}
func TestSnapshotRegistryCompletion(t *testing.T) {
	f := newFixture(t)
	refs := []ids.TxRef{f.tx(), f.tx(), f.tx()}
	for _, ref := range refs {
		if err := f.reg.AssignTransactionXIDLocked(ref); err != nil {
			t.Fatal(err)
		}
	}
	f.reg.EndTransactionIdentityLocked(refs[2], false)
	f.reg.EndTransactionIdentityLocked(refs[0], true)
	captured, err := f.reg.CurrentSnapshotLocked(f.tx())
	if err != nil || captured.String() != "4:6:4" {
		t.Fatal(captured, err)
	}
	// A duplicate end cannot change completion or regress the full-XID frontier.
	f.reg.EndTransactionIdentityLocked(refs[2], true)
	if f.reg.History[5].State != ids.TxAborted || f.reg.latestCompletedXID != 5 {
		t.Fatal("completion changed")
	}
	f.reg.latestCompletedXID = 1<<32 - 1
	wrapped, err := f.reg.CurrentSnapshotLocked(f.tx())
	if err != nil || wrapped.Xmax() != 1<<32|3 {
		t.Fatal(wrapped, err)
	}
	f.reg.latestCompletedXID = ^uint64(0)
	if _, err := f.reg.CurrentSnapshotLocked(f.tx()); err == nil {
		t.Fatal("exhausted snapshot frontier accepted")
	}
	if f.reg.latestCompletedXID != ^uint64(0) {
		t.Fatal("capture changed registry")
	}
}
