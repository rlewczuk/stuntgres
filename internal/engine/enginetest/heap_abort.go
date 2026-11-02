package enginetest

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// HeapAbortSnapshot separates immutable committed roots from genuine aborted
// slots. Capture it before disposal, after any admitted physical header events.
type HeapAbortSnapshot struct {
	store   *storage.Relation
	before  storage.Relation
	rows    []storage.RowVersion
	ends    *storage.DeletionRoot
	headers *storage.HeapHeaderRoot
}

func CaptureHeapAbort(store *storage.Relation) HeapAbortSnapshot {
	rows := slices.Clone(store.Versions)
	for i := range rows {
		rows[i].Values = slices.Clone(rows[i].Values)
	}
	var headers *storage.HeapHeaderRoot
	if store.Headers != nil {
		copy := *store.Headers
		copy.Additional = slices.Clone(copy.Additional)
		headers = &copy
	}
	return HeapAbortSnapshot{store, *store, rows, store.Ends.Clone(), headers}
}

// AssertHeapAbort permits only the specified number of genuine disposal
// publications, never arbitrary generation/root changes or committed rows.
func AssertHeapAbort(t testing.TB, store *storage.Relation, before HeapAbortSnapshot, publications uint64, created ...storage.RowVersion) {
	t.Helper()
	if store != before.store {
		t.Fatal("abort replaced the installed heap incarnation")
	}
	generation := before.before.Generation + publications
	if (publications == 0) != (len(created) == 0) {
		t.Fatal("disposal count does not match created-version witness")
	}
	if store.Generation != generation || !storage.SameVersionRoot(store.Versions, before.before.Versions) || store.Ends != before.before.Ends || store.Headers != before.before.Headers || !reflect.DeepEqual(store.Versions, before.rows) || !reflect.DeepEqual(store.Ends, before.ends) || !reflect.DeepEqual(store.Headers, before.headers) {
		t.Fatal("abort changed committed rows/ends/headers or the exact disposal generation")
	}
	copy := *store
	copy.Generation, copy.Discarded = before.before.Generation, before.before.Discarded
	if !reflect.DeepEqual(copy, before.before) {
		t.Fatal("abort changed unrelated heap authority")
	}
	assertDiscardedSuffix(t, store.Discarded, before.before.Discarded, created)
	for _, want := range created {
		got, _, ok := store.PhysicalVersion(want.Version, -1)
		if !ok || got.Born != 0 || got.ForcedFrozen || got.HeapXmin() != uint32(want.InsertFullXID) || got.HeapXmax() != uint32(want.DeleteFullXID) {
			t.Fatal("discarded physical slot lost raw aborted provenance", got, want)
		}
	}
}

// AssertJournalDiscards preserves every non-disposal journal field. The only
// extra authority is the exact set of captured created versions in this heap.
func AssertJournalDiscards(t testing.TB, journal, prefix *session.Journal, address ids.ObjectAddress, store *storage.Relation, created ...storage.RowVersion) {
	t.Helper()
	got, want := journal.Clone(), prefix.Clone()
	got.DiscardedHeaps, want.DiscardedHeaps = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatal("savepoint changed the retained catalog/provider/storage journal")
	}
	var rows []storage.RowVersion
	for _, batch := range journal.DiscardedHeaps {
		if batch.Address != address || batch.Storage != store {
			t.Fatal("savepoint retargeted discarded slots to another heap")
		}
		for prior := batch.Versions; prior != nil; prior = prior.Next {
			if prior.End != 0 || prior.EndFullXID != 0 {
				t.Fatal("savepoint discarded slot acquired a committed end")
			}
			rows = append(rows, prior.Version)
		}
	}
	var chain *storage.HeapPredecessor
	for _, row := range rows {
		chain = &storage.HeapPredecessor{Version: row, Next: chain}
	}
	assertDiscardedSuffix(t, chain, nil, created)
}

func assertDiscardedSuffix(t testing.TB, root, prefix *storage.HeapPredecessor, created []storage.RowVersion) {
	t.Helper()
	remaining := make(map[ids.VersionID]storage.RowVersion, len(created))
	for _, row := range created {
		if row.Version == 0 || row.Born != 0 || row.InsertFullXID == 0 {
			t.Fatal("abort witness is not an actual created private version", row)
		}
		if _, exists := remaining[row.Version]; exists {
			t.Fatal("abort witness repeats a created version", row.Version)
		}
		remaining[row.Version] = row
	}
	for root != prefix {
		if root == nil {
			t.Fatal("abort lost the old discarded suffix")
		}
		want, ok := remaining[root.Version.Version]
		if !ok {
			t.Fatal("abort retained an unexpected or duplicate physical version", root.Version.Version)
		}
		delete(remaining, want.Version)
		want.PrivateHistory, want.IndexAdmission, want.IndexOutputs = nil, nil, nil
		if root.End != 0 || root.EndFullXID != 0 || !reflect.DeepEqual(root.Version, want) || len(want.Values) != 0 && &root.Version.Values[0] != &want.Values[0] {
			t.Fatal("abort changed exact version/header/payload provenance", root.Version, want)
		}
		root = root.Next
	}
	if len(remaining) != 0 {
		t.Fatal("abort lost genuine created slots", remaining)
	}
}
