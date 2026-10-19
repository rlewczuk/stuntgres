package visibility

import "testing"

// readyClosed reports whether ch is closed without blocking.
func readyClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// A DEFERRABLE read-only capture waits for the overlapping read-write writers
// and resolves safe once the last of them commits without unsafe conflicts.
func TestSafeSnapshotWaitResolvesSafe(t *testing.T) {
	f := newFixture(t)
	writer := f.node(false)
	reader := f.tx()
	f.reg.EnrollSerializableLocked(reader, true)
	node := f.reg.CaptureSerializableLocked(reader, true, true)
	ready, wait := node.SafeSnapshotWait()
	if !wait || ready == nil || readyClosed(ready) {
		t.Fatal("capture with a live writer does not wait")
	}
	if blockers := f.reg.SafeSnapshotBlockersLocked(f.domain, f.epoch, reader.Session()); len(blockers) != 1 || blockers[0].Session != writer.Ref.Session() {
		t.Fatal("blocker list does not name the writer", blockers)
	}
	f.reg.SerializableCommittedLocked(writer.Ref, f.reg.CommitSeq())
	if !readyClosed(ready) {
		t.Fatal("writer commit did not wake the waiter")
	}
	if live, safe := node.SafeSnapshotOutcome(); !live || !safe {
		t.Fatal("clean resolution is not live and safe", live, safe)
	}
	if _, wait := node.SafeSnapshotWait(); wait {
		t.Fatal("resolved capture waits again")
	}
}

// Without overlapping writers, or without DEFERRABLE, a capture never waits.
func TestSafeSnapshotWaitImmediate(t *testing.T) {
	for _, deferrable := range []bool{false, true} {
		f := newFixture(t)
		if !deferrable {
			f.node(false) // a live writer does not matter without DEFERRABLE
		}
		reader := f.tx()
		f.reg.EnrollSerializableLocked(reader, true)
		node := f.reg.CaptureSerializableLocked(reader, true, deferrable)
		if _, wait := node.SafeSnapshotWait(); wait {
			t.Fatal("capture waits", deferrable)
		}
	}
}

// Abandoning a failed acquisition resolves it once and drops its writers;
// a later end of the same node must not close the channel again.
func TestSafeSnapshotAbandon(t *testing.T) {
	f := newFixture(t)
	f.node(false)
	reader := f.tx()
	f.reg.EnrollSerializableLocked(reader, true)
	node := f.reg.CaptureSerializableLocked(reader, true, true)
	ready, _ := node.SafeSnapshotWait()
	node.AbandonSafeSnapshotLocked()
	if !readyClosed(ready) || !node.Resolved || node.Possible != nil {
		t.Fatal("abandon did not resolve the acquisition")
	}
	node.AbandonSafeSnapshotLocked()
	f.reg.EndTransactionLocked(reader, false)
	if live, _ := node.SafeSnapshotOutcome(); live {
		t.Fatal("ended node reports live")
	}
}

// Fencing the registry wakes a waiting reader and ends its liveness.
func TestSafeSnapshotFenceWakesWaiter(t *testing.T) {
	f := newFixture(t)
	f.node(false)
	reader := f.tx()
	f.reg.EnrollSerializableLocked(reader, true)
	node := f.reg.CaptureSerializableLocked(reader, true, true)
	ready, _ := node.SafeSnapshotWait()
	f.reg.FenceLocked()
	if !readyClosed(ready) {
		t.Fatal("fence did not wake the waiter")
	}
	if live, _ := node.SafeSnapshotOutcome(); live {
		t.Fatal("fenced node reports live")
	}
}
