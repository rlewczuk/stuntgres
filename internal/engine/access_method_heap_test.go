package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func accessMethodHeapOperationsUnit(t *testing.T, view catalog.Read) visibility.HeapOperations {
	t.Helper()
	heap, err := visibility.ResolveHeapAccess(view, 2)
	if err != nil {
		t.Fatal(err)
	}
	return heap
}

func TestAccessMethodHeapOperations(t *testing.T) {
	e := New()
	custom := accessMethodUnit(t, e, "am_heap_operations", catalog.HeapAccessMethod, 3)
	view := accessMethodPublish(t, e, custom)
	heap := accessMethodHeapOperationsUnit(t, view)
	customHeap, err := visibility.ResolveHeapAccess(view, custom.Address().ObjectID)
	if err != nil || customHeap != heap {
		t.Fatal("registered TABLE method did not select ordinary heap operations", err)
	}
	for _, native := range []catalog.NativeAccessMethod{catalog.NoAccessMethod, catalog.BtreeAccessMethod, catalog.HashAccessMethod, catalog.GistAccessMethod, 255} {
		if _, ok := visibility.ResolveTableAccess(native); ok {
			t.Fatal("nonheap selector resolved table operations", native)
		}
	}
	for _, oid := range []ids.OID{0, 403, 405, 783} {
		if _, err := visibility.ResolveHeapAccess(view, oid); err == nil {
			t.Fatal("missing/index method granted heap authority", oid)
		}
	}
	handler, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3})
	for _, change := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.AccessMethod = catalog.NoAccessMethod },
		func(p *catalog.Procedure) { p.ObjectName = "forged_heap_handler" },
		func(p *catalog.Procedure) { p.Strict = false },
	} {
		forged := *handler
		change(&forged)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(forged.Address(), &forged)
		if _, err := visibility.ResolveHeapAccess(view.WithState(state), custom.Address().ObjectID); err == nil {
			t.Fatal("Heap marker without genuine installed handler granted authority")
		}
	}
	missing := catalog.CloneState(view.State())
	missing.RemoveDescriptor(handler.Address())
	if _, err := visibility.ResolveHeapAccess(view.WithState(missing), custom.Address().ObjectID); err == nil {
		t.Fatal("absent handler granted heap authority")
	}

	store := &storage.Relation{
		Versions: []storage.RowVersion{
			{Row: 1, Version: 11},
			{Row: 2, Version: 22, Born: 6},
			{Row: 3, Version: 33, Born: 5},
			{Row: 4, Version: 44, Born: 1},
			{Row: 5, Version: 55, Born: 1},
			{Row: 6, Version: 66, Born: 1},
			{Row: 7, Version: 77, Born: 1},
			{Row: 8, Version: 88, Born: 1},
			{Row: 9, Version: 99, Born: 1},
		},
		Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{
			4: {Version: 44, End: 6}, 5: {Version: 55, End: 5}, 6: {Version: 67, End: 4},
		}},
	}
	own := &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{
		7: {Row: 7, Version: 77}, 8: {Row: 8, Version: 89, Base: 88}, 9: {Row: 9, Version: 100, Base: 101},
	}}
	before := append([]storage.RowVersion(nil), store.Versions...)
	beforeEnds, beforeDeletes := maps.Clone(store.Ends.Rows), maps.Clone(own.Rows)
	for i, tc := range []struct {
		name string
		live bool
	}{
		{"private insert", true}, {"future birth", false}, {"birth at frontier", true},
		{"end after frontier", true}, {"end at frontier", false}, {"different ended version", true},
		{"own delete", false}, {"replacement base", false}, {"same row different lineage", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := store.Versions[i]
			if heap.LiveVersion(v, 5, store.Ends, own) != tc.live || customHeap.LiveVersion(v, 5, store.Ends, own) != tc.live || visibility.LiveVersion(v, 5, store.Ends, own) != tc.live {
				t.Fatal("ordinary/registered heap liveness", v, tc.live)
			}
		})
	}
	source := &exec.Source{Committed: store.Versions, Own: []storage.RowVersion{{Row: 3, Version: 303, Base: 33}, {Row: 10, Version: 110}}, NeedsBirthFilter: true}
	beforeOwn := append([]storage.RowVersion(nil), source.Own...)
	selected, err := exec.SelectQuerySource(context.Background(), source, 5, store.Ends, own, heap)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	if !reflect.DeepEqual(selected.Visible, []int{0, 9, 3, 5, 8, 10}) {
		t.Fatal("real heap source selection", selected.Visible)
	}
	var versions []ids.VersionID
	for i := range selected.Count() {
		versions = append(versions, selected.VersionAt(selected.RawIndex(i)).Version)
	}
	if !reflect.DeepEqual(versions, []ids.VersionID{11, 303, 44, 66, 99, 110}) || !reflect.DeepEqual(store.Versions, before) || &selected.Committed[0] != &store.Versions[0] {
		t.Fatal("selection lost live identities or mutated/copied retained storage", versions)
	}
	prefix, err := exec.SelectQuerySource(context.Background(), &exec.Source{Committed: store.Versions[:1]}, 5, nil, nil, heap)
	if err != nil || prefix.Visible != nil || &prefix.Committed[0] != &store.Versions[0] {
		t.Fatal("unfiltered prefix lost allocation-free borrowing", err)
	}
	prefix.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled, err := exec.SelectQuerySource(ctx, &exec.Source{Committed: store.Versions}, 5, store.Ends, own, heap)
	if canceled != nil || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(store.Versions, before) {
		t.Fatal("heap selection cancellation mutated retained root", err)
	}
	if !reflect.DeepEqual(store.Ends.Rows, beforeEnds) || !reflect.DeepEqual(own.Rows, beforeDeletes) || !reflect.DeepEqual(source.Own, beforeOwn) {
		t.Fatal("heap selection mutated retained ends/private overlays")
	}
}
