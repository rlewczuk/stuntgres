package session

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// dataViewFixture is one published relation with its storage, read through a
// nil journal.
type dataViewFixture struct {
	table  *catalog.Relation
	ref    ids.ObjectRef
	stored *storage.Relation
	in     DataViewInput
}

func newDataViewFixture(t *testing.T, id ids.OID) *dataViewFixture {
	t.Helper()
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	table := journalTestRelation(id, "t", namespace)
	table.Attributes = []int32{1}
	state := journalTestState(table)
	stored := &storage.Relation{Versions: make([]storage.RowVersion, 3), Ends: &storage.DeletionRoot{}, Generation: 1}
	f := &dataViewFixture{table: table, ref: ids.ObjectRef{Address: table.Address()}, stored: stored}
	f.in = DataViewInput{
		Published: state, Store: &storage.Store{Relations: map[ids.ObjectAddress]*storage.Relation{table.Address(): stored}},
		Read:            catalog.NewRead(nil, state),
		SameLineage:     func(_, _ *catalog.Relation, _, _ *storage.Relation) bool { return false },
		SameStableReadLineage: func(_, _ *catalog.Relation, _, _ *storage.Relation) bool { return false },
		CrossedTruncate: func(_, _ *catalog.Relation, _, _ *storage.Relation) bool { return false },
		SameRowShape:    func(a, b *catalog.Relation) bool { return a == b },
	}
	return f
}

func (f *dataViewFixture) capture(t *testing.T, refs ...ids.ObjectRef) *visibility.Data {
	t.Helper()
	view, err := f.in.Capture(refs)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	return view.Data()
}

func TestDataViewValidate(t *testing.T) {
	f := newDataViewFixture(t, 20200)
	captured := func(stored *storage.Relation) *visibility.View {
		return visibility.NewView(&visibility.Data{
			Catalog:          f.in.Read,
			Committed:        map[ids.ObjectAddress][]storage.RowVersion{f.ref.Address: nil},
			SingleGeneration: stored,
		})
	}
	if err := f.in.ValidateView(captured(f.stored), []ids.ObjectRef{f.ref}); err != nil {
		t.Fatalf("current generation: %v", err)
	}
	released := captured(f.stored)
	released.Release()
	if err := f.in.ValidateView(released, []ids.ObjectRef{f.ref}); err == nil || errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("released view error = %v, want a released-view error", err)
	}
	outside := ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20201}}
	if err := f.in.ValidateView(captured(f.stored), []ids.ObjectRef{outside}); err == nil || errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("outside relation error = %v, want an outside-view error", err)
	}
	old := &storage.Relation{Generation: 0}
	if err := f.in.ValidateView(captured(old), []ids.ObjectRef{f.ref}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("replaced generation error = %v, want ErrCatalogReplaced", err)
	}
	f.in.SameLineage = func(o, c *catalog.Relation, a, b *storage.Relation) bool {
		return o == f.table && c == f.table && a == old && b == f.stored
	}
	if err := f.in.ValidateView(captured(old), []ids.ObjectRef{f.ref}); err != nil {
		t.Fatalf("same materialized lineage: %v", err)
	}
	for _, metadata := range []string{"name", "owner"} {
		changed := *f.table
		if metadata == "name" {
			changed.ObjectName = "renamed"
		} else {
			changed.Owner = (&authorization.Domain{}).Principal(10)
		}
		f.in.Published = journalTestState(&changed)
		f.in.SameStableReadLineage = func(_, _ *catalog.Relation, _, _ *storage.Relation) bool {
			t.Fatal("ValidateView consulted new-query routing")
			return true
		}
		if err := f.in.ValidateView(captured(old), []ids.ObjectRef{f.ref}); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("compatible new-read metadata licensed an already captured view", metadata, err)
		}
	}
}

func TestDataViewCaptureReadCommitted(t *testing.T) {
	f := newDataViewFixture(t, 20210)
	f.in.Frontier, f.in.CommitSeq = 4, 5
	data := f.capture(t, f.ref)
	if len(data.Committed[f.ref.Address]) != 3 || data.SingleGeneration != f.stored || !data.SingleNeedsBirthFilter || data.Ends[f.ref.Address] != f.stored.Ends {
		t.Fatalf("RC capture = %+v, want live rows with a birth filter", data)
	}
	target := visibility.Root{Versions: make([]storage.RowVersion, 1), Storage: f.stored, Relation: f.table}
	f.in.Target = target
	data = f.capture(t, f.ref)
	if len(data.Committed[f.ref.Address]) != 1 || data.SingleNeedsBirthFilter || data.Ends != nil {
		t.Fatalf("RC target capture = %+v, want the admitted target root", data)
	}
	f.in.Target.Storage = &storage.Relation{}
	if _, err := f.in.Capture([]ids.ObjectRef{f.ref}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("replaced target error = %v, want ErrCatalogReplaced", err)
	}
	f.in.Target, f.in.Targets = visibility.Root{}, map[ids.ObjectAddress]visibility.Root{f.ref.Address: target}
	if data = f.capture(t, f.ref); len(data.Committed[f.ref.Address]) != 1 {
		t.Fatalf("additional target capture = %+v, want the admitted target root", data)
	}
}

func TestDataViewCaptureStable(t *testing.T) {
	f := newDataViewFixture(t, 20220)
	f.in.Stable = true
	data := f.capture(t, f.ref)
	if data.Committed[f.ref.Address] != nil || data.Ends != nil {
		t.Fatalf("rootless stable capture = %+v, want no committed rows", data)
	}
	root := visibility.Root{Versions: make([]storage.RowVersion, 2), Ends: &storage.DeletionRoot{}, Storage: f.stored, Relation: f.table}
	f.in.Roots = map[ids.ObjectAddress]visibility.Root{f.ref.Address: root}
	data = f.capture(t, f.ref)
	if len(data.Committed[f.ref.Address]) != 2 || data.Ends[f.ref.Address] != root.Ends || data.SingleNeedsBirthFilter {
		t.Fatalf("stable capture = %+v, want the retained root", data)
	}
	snapshot := make([]storage.RowVersion, 4)
	f.in.Journal = &Journal{Storage: map[ids.ObjectAddress]StorageChange{f.ref.Address: {Replacement: f.stored, HasSnapshot: true, Snapshot: snapshot}}}
	if data = f.capture(t, f.ref); len(data.Committed[f.ref.Address]) != 4 {
		t.Fatalf("projected capture = %+v, want the projected snapshot", data)
	}
	f.in.Journal = nil
	f.in.Roots = map[ids.ObjectAddress]visibility.Root{f.ref.Address: {Storage: f.stored, Relation: &catalog.Relation{}}}
	if _, err := f.in.Capture([]ids.ObjectRef{f.ref}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("changed row shape error = %v, want ErrCatalogReplaced", err)
	}
	// The new-command policy is distinct from validating an already captured
	// view: a genuine newer TRUNCATE boundary uses current roots, not old rows.
	f.in.Roots = map[ids.ObjectAddress]visibility.Root{f.ref.Address: root}
	f.in.Frontier = 4
	f.stored.RetainedOrigin = 20220
	current := &storage.Relation{Versions: make([]storage.RowVersion, 1), Ends: &storage.DeletionRoot{}, RetainedOrigin: 20220, TruncateBoundary: 5}
	f.in.Store.Relations[f.ref.Address] = current
	f.in.SameStableReadLineage = func(_, _ *catalog.Relation, a, b *storage.Relation) bool {
		return a.RetainedOrigin == b.RetainedOrigin && a.TruncateBoundary == b.TruncateBoundary
	}
	f.in.CrossedTruncate = func(o, c *catalog.Relation, a, b *storage.Relation) bool {
		return o == f.table && c == f.table && a == root.Storage && b == current && a.RetainedOrigin == b.RetainedOrigin && b.TruncateBoundary > a.TruncateBoundary
	}
	data = f.capture(t, f.ref)
	if !storage.SameVersionRoot(data.Committed[f.ref.Address], current.Versions) || data.Ends[f.ref.Address] != current.Ends || !data.SingleNeedsBirthFilter || data.Frontier != 4 {
		t.Fatal("crossed TRUNCATE did not retain frontier with current roots", data)
	}
	oldView := visibility.NewView(&visibility.Data{Catalog: f.in.Read, Committed: map[ids.ObjectAddress][]storage.RowVersion{f.ref.Address: root.Versions}, SingleGeneration: root.Storage})
	defer oldView.Release()
	if err := f.in.ValidateView(oldView, []ids.ObjectRef{f.ref}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("current-root query policy licensed an old captured view", err)
	}
	sameBoundary := *current
	sameBoundary.TruncateBoundary = 0
	current = &sameBoundary
	f.in.Store.Relations[f.ref.Address] = current
	data = f.capture(t, f.ref)
	if !storage.SameVersionRoot(data.Committed[f.ref.Address], root.Versions) || data.Ends[f.ref.Address] != root.Ends || data.SingleNeedsBirthFilter {
		t.Fatal("equal boundary did not retain actual old roots", data)
	}
	// Equal-boundary new binding tolerates compatible name metadata without
	// consulting strict captured-view authority or selecting current rows.
	renamed := *f.table
	renamed.ObjectName = "renamed"
	f.in.Published = journalTestState(&renamed)
	f.in.Read = catalog.NewRead(nil, f.in.Published)
	f.in.SameRowShape = func(a, b *catalog.Relation) bool { return a.Address() == b.Address() }
	f.in.SameLineage = func(_, _ *catalog.Relation, _, _ *storage.Relation) bool {
		t.Fatal("new stable query consulted captured-view authority")
		return false
	}
	data = f.capture(t, f.ref)
	if !storage.SameVersionRoot(data.Committed[f.ref.Address], root.Versions) || data.Ends[f.ref.Address] != root.Ends || data.Frontier != 4 || data.SingleNeedsBirthFilter {
		t.Fatal("compatible new query did not select exact old root/frontier", data)
	}
}

func TestDataViewCaptureOwnState(t *testing.T) {
	f := newDataViewFixture(t, 20230)
	other := journalTestRelation(20231, "u", f.table.Namespace())
	f.in.Published = journalTestState(f.table, other)
	f.in.Read = catalog.NewRead(nil, f.in.Published)
	f.in.Store.Relations[other.Address()] = &storage.Relation{}
	otherRef := ids.ObjectRef{Address: other.Address()}
	f.in.OwnWrites = map[ids.ObjectAddress][]storage.RowVersion{f.ref.Address: make([]storage.RowVersion, 1)}
	f.in.OwnDeletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{f.ref.Address: {}, other.Address(): {}}
	data := f.capture(t, f.ref, otherRef)
	if len(data.Generations) != 2 || data.Generations[f.ref.Address].Storage != f.stored || len(data.Own[f.ref.Address]) != 1 {
		t.Fatalf("multi-ref capture = %+v, want per-relation generations and own writes", data)
	}
	if reflect.ValueOf(data.Deleted).UnsafePointer() != reflect.ValueOf(f.in.OwnDeletes).UnsafePointer() {
		t.Fatalf("deletes = %v, want the statement's exact map reused", data.Deleted)
	}
	data = f.capture(t, f.ref)
	if len(data.Deleted) != 1 || data.Deleted[f.ref.Address] != f.in.OwnDeletes[f.ref.Address] {
		t.Fatalf("subset deletes = %v, want only the selected relation", data.Deleted)
	}
}
