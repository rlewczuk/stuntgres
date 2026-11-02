package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestIndexPrivateDeletionVersionCapture(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	value, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	first := storage.RowVersion{Row: 7, Version: 1, Born: 1, Values: []sqltype.Value{value}}
	second := storage.RowVersion{Row: 7, Version: 2, Born: 2, Values: first.Values}
	store := &storage.Relation{Versions: []storage.RowVersion{second}, Retained: &storage.HeapPredecessor{Version: first, End: 2}}
	ordinary, err := index.BuildVisibleStore(context.Background(), store, nil, nil)
	if err != nil || ordinary != store {
		t.Fatal("unchanged ordinary capture lost identical no-copy root", err)
	}
	before := *store
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{first})
	if err != nil {
		t.Fatal(err)
	}
	store.Headers = headers
	assert := func(capture *storage.Relation, versions ...ids.VersionID) {
		t.Helper()
		want := make(map[ids.VersionID]bool, len(versions))
		for _, version := range versions {
			want[version] = true
		}
		for _, row := range capture.Versions {
			if !want[row.Version] || row.Row != first.Row || &row.Values[0] != &first.Values[0] {
				t.Fatal("capture duplicated/rebound sibling or copied immutable payload", row.Version)
			}
			delete(want, row.Version)
		}
		if len(want) != 0 {
			t.Fatal("capture lost exact physical sibling", want)
		}
	}
	repaired, err := index.BuildVisibleStore(context.Background(), store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assert(repaired, 1, 2)
	pre := storage.RowVersion{Row: 7, Version: 3, Base: 1, Values: first.Values, PrivateHistory: storage.RetainHeapPredecessor(first)}
	preCapture, err := index.BuildVisibleStore(context.Background(), store, []storage.RowVersion{pre}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assert(preCapture, 1, 2, 3)
	post := pre
	post.Version = 4
	post.PrivateHistory = storage.RetainHeapPredecessor(headers.Apply(first))
	postCapture, err := index.BuildVisibleStore(context.Background(), store, []storage.RowVersion{post}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assert(postCapture, 2, 4)
	private := storage.RowVersion{Row: 7, Version: 5, Base: 1, Values: first.Values, PrivateHistory: storage.RetainHeapPredecessor(first)}
	privateHeaders, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{private})
	if err != nil {
		t.Fatal(err)
	}
	last := storage.RowVersion{Row: 7, Version: 6, Base: 5, Values: first.Values, PrivateHistory: storage.RetainHeapPredecessor(privateHeaders.Apply(private))}
	chainStore := &storage.Relation{Versions: []storage.RowVersion{first}, Headers: privateHeaders}
	chainCapture, err := index.BuildVisibleStore(context.Background(), chainStore, []storage.RowVersion{last}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assert(chainCapture, 6)
	deletes := &visibility.PrivateDeletes{}
	deletes.Set(visibility.DeleteCandidate{Row: 7, Version: 1, RepairStamp: headers.Header(1).RepairStamp})
	deletes.Set(visibility.DeleteCandidate{Row: 7, Version: 2})
	deleted, err := index.BuildVisibleStore(context.Background(), store, nil, deletes)
	if err != nil {
		t.Fatal(err)
	}
	assert(deleted)
	if !index.UniqueRowDeleted(headers.Apply(first), deletes) || !index.UniqueRowDeleted(second, deletes) || index.UniqueRowDeleted(pre, deletes) || index.UniqueRowDeleted(storage.RowVersion{Row: 8, Version: 1}, deletes) {
		t.Fatal("private deletion existence fell back to shared RowID")
	}
	if before.Headers != nil || before.Versions[0].Version != 2 || headers.Additional[0].ForcedFrozen || first.ForcedFrozen || &before.Versions[0].Values[0] != &first.Values[0] {
		t.Fatal("index capture mutated old immutable heap/header backing")
	}
}
