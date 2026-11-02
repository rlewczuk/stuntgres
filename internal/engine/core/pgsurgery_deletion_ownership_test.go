package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestPgsurgeryFreezeDeletionOwnership(t *testing.T) {
	e, sess, ref, store := freezeRetentionFixture(t)
	original := store.Versions[0]
	oldCapture := *store
	enginetest.Run(t, e, "UPDATE freeze_retention SET s='successor'")
	successor := store.Versions[0]
	if original.Row != successor.Row || original.Version == successor.Version {
		t.Fatal("setup did not create genuine same-row physical successor")
	}
	// The nested DELETE needs a genuine committed end, not an inserted
	// private tuple with no committed predecessor.
	enginetest.Run(t, e, "INSERT INTO freeze_retention VALUES(2,'child')")
	if len(store.Versions) != 2 {
		t.Fatal("setup did not retain successor and committed child")
	}
	child := store.Versions[1]
	if child.Born == 0 || child.Row == original.Row || child.Version == original.Version || child.Version == successor.Version {
		t.Fatal("setup did not create an unrelated committed child")
	}
	e.Mu.Lock()
	headers, err := storage.RepairHeapHeaders(store.Headers, []storage.RowVersion{original})
	if err == nil {
		store.Headers = headers
		store.Generation++
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, sess, "BEGIN")
	scope, finish := freezeRetentionScope(t, e, sess)
	freezeRetentionExecute(t, scope, "DELETE FROM freeze_retention WHERE n=1")
	root := scope.Stage.Deletes[ref.Address]
	if root.Len() != 2 {
		t.Fatal("actual sibling DELETE did not retain both admitted physical candidates")
	}
	first, firstOK := root.Lookup(original.Row, original.Version)
	second, secondOK := root.Lookup(successor.Row, successor.Version)
	if !firstOK || !secondOK || first.Version != original.Version || second.Version != successor.Version || first.Storage != store || second.Storage != store || first.EndFullXID == 0 || first.EndFullXID != second.EndFullXID {
		t.Fatal("actual sibling deletion headers lost exact target/owner provenance")
	}
	e.Mu.Lock()
	ownerXID := e.Visibility.FullXIDLocked(scope.TxRef)
	e.Mu.Unlock()
	if first.EndFullXID != ownerXID {
		t.Fatal("sibling deletion did not retain the actual admitted owner")
	}
	captureDeletes := func() *visibility.PrivateDeletes {
		t.Helper()
		restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
		view, err := scope.AcquireDataView(ref)
		if err != nil {
			_ = restore(err)
			t.Fatal(err)
		}
		retained := view.Retain()
		if err := restore(nil); err != nil {
			retained.Release()
			t.Fatal(err)
		}
		t.Cleanup(retained.Release)
		return retained.Data().Deleted[ref.Address]
	}
	assertSiblings := func(captured *visibility.PrivateDeletes) {
		t.Helper()
		if actual, ok := captured.Lookup(original.Row, original.Version); !ok || actual != first {
			t.Fatal("captured primary deletion lost exact sibling provenance")
		}
		if actual, ok := captured.Lookup(successor.Row, successor.Version); !ok || actual != second {
			t.Fatal("captured supplementary deletion lost exact sibling provenance")
		}
	}
	capturedRoot := captureDeletes()
	if capturedRoot == root || capturedRoot.Len() != 2 {
		t.Fatal("immutable data view borrowed the owner-mutable deletion stage")
	}
	assertSiblings(capturedRoot)
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	freezeRetentionNested(t, scope, "DELETE FROM freeze_retention WHERE n=2")
	childRoot := scope.Stage.Deletes[ref.Address]
	if childRoot.Len() != 3 || capturedRoot.Len() != 2 {
		t.Fatal("nested committed deletion lost an end or mutated the retained two-end capture")
	}
	third, ok := childRoot.Lookup(child.Row, child.Version)
	if !ok || third.Ref != ref || third.Storage != store || third.Row != child.Row || third.Version != child.Version || third.Own || third.Base != 0 || third.EndFullXID != ownerXID || third.RepairStamp != child.RepairStamp || third.BaseRepairStamp != 0 {
		t.Fatal("nested child deletion lost exact committed target/owner provenance")
	}
	assertSiblings(childRoot)
	assertSiblings(capturedRoot)
	childCapture := captureDeletes()
	if childCapture == childRoot || childCapture.Len() != 3 {
		t.Fatal("nested data view did not own its immutable three-end capture")
	}
	if err := scope.EndPLException(mark, true, errors.New("caught child failure")); err != nil {
		t.Fatal(err)
	}
	restored := scope.Stage.Deletes[ref.Address]
	if restored == childRoot || restored.Len() != 2 {
		t.Fatal("protected rewind did not restore both sibling deletion candidates")
	}
	if actual, ok := restored.Lookup(original.Row, original.Version); !ok || actual != first {
		t.Fatal("exception checkpoint cloned only primary deletion map")
	}
	if actual, ok := restored.Lookup(successor.Row, successor.Version); !ok || actual != second {
		t.Fatal("exception checkpoint lost supplementary sibling end")
	}
	if _, ok := restored.Lookup(child.Row, child.Version); ok {
		t.Fatal("exception rewind retained discarded child deletion")
	}
	if capturedRoot.Len() != 2 || childRoot.Len() != 3 || childCapture.Len() != 3 {
		t.Fatal("protected rewind mutated retained two- or three-end captures")
	}
	assertSiblings(capturedRoot)
	assertSiblings(childRoot)
	assertSiblings(childCapture)
	if _, ok := capturedRoot.Lookup(child.Row, child.Version); ok {
		t.Fatal("old immutable capture acquired the nested child end")
	}
	if actual, ok := childRoot.Lookup(child.Row, child.Version); !ok || actual != third {
		t.Fatal("rewind mutated the discarded three-end stage")
	}
	if actual, ok := childCapture.Lookup(child.Row, child.Version); !ok || actual != third {
		t.Fatal("rewind mutated the retained child deletion capture")
	}
	retainedChild, _, ok := store.PhysicalVersion(child.Version, -1)
	if !ok || retainedChild.Row != child.Row || retainedChild.Born != child.Born || &retainedChild.Values[0] != &child.Values[0] {
		t.Fatal("rewind lost the committed child's identity or immutable payload")
	}
	if oldCapture.Headers != nil || oldCapture.Versions[0].Version != original.Version || &oldCapture.Versions[0].Values[0] != &original.Values[0] || &store.Versions[0].Values[0] != &successor.Values[0] || childRoot.Len() != 3 {
		t.Fatal("owner rewind mutated old immutable heap/deletion captures")
	}
	primary := errors.New("owner completion")
	if err := finish(primary); err != primary {
		t.Fatal("owner completion replaced primary failure", err)
	}
}
