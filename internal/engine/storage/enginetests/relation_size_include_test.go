package enginetests

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestRelationSizePrivateIncludePrefix(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() { types[typ.ID()] = typ }
	key, err := sqltype.Integer(types[sqltype.Int4], 1)
	if err != nil { t.Fatal(err) }
	values := []sqltype.Value{key, sqltype.String(types[sqltype.Text], "included")}
	keys := []sqltype.Value{key}
	const publishedCount, privateCount = 8192, 2048
	published := make([]storage.RowVersion, publishedCount)
	entries := make([]storage.IndexEntry, publishedCount)
	for i := range published {
		published[i] = storage.RowVersion{Row: ids.RowID(i+1), Version: ids.VersionID(i+1), Values: values}
		entries[i] = storage.IndexEntry{Row: published[i].Row, Version: published[i].Version, Position: i, Keys: keys}
	}
	private := make([]storage.RowVersion, privateCount)
	for i := range private {
		private[i] = storage.RowVersion{Row: ids.RowID(publishedCount+i+1), Version: ids.VersionID(publishedCount+i+1), Values: values}
	}
	a := storage.Allocation{
		Allocated: true,
		Relation: &catalog.Relation{SQLIndex: &catalog.IndexDefinition{Keys: []catalog.IndexKey{{Attribute: 2}}, Include: []int32{7}}},
		Parent: &catalog.Relation{Attributes: []int32{2, 7}},
		ParentVersions: published,
		ParentOwn: private,
		Index: &storage.IndexRoot{Entries: entries},
	}
	got, err := a.Forks(context.Background())
	// Every entry owns header16 + fixed key8 + included variable slot32.
	want := int64(64 + (publishedCount+privateCount)*56)
	if err != nil || got.Main != want { t.Fatal("published and private INCLUDE allocations", got, want, err) }
	if len(published) != publishedCount || len(private) != privateCount || published[0].Values[1] != values[1] || private[0].Values[1] != values[1] {
		t.Fatal("allocation changed borrowed roots or datums")
	}

	// A captured entry can require a fallback after a stale position hint.
	// Cancel deterministically inside that long identity search, not on a timer
	// or at the next entry. The old unpolled lookup could not observe this.
	a.Index = &storage.IndexRoot{Entries: []storage.IndexEntry{{Row: published[publishedCount-1].Row, Version: published[publishedCount-1].Version, Position: -1, Keys: keys}}}
	a.ParentOwn = nil
	if got, err := a.Forks(context.Background()); err != nil || got.Main != 120 { t.Fatal("stale position must find the exact row version", got, err) }
	entry := a.Index.Entries[0]
	entry.Version++
	a.Index = &storage.IndexRoot{Entries: []storage.IndexEntry{entry}}
	if _, err := a.Forks(context.Background()); err == nil { t.Fatal("wrong version adopted an unrelated included row") }
	entry.Version--
	a.Index = &storage.IndexRoot{Entries: []storage.IndexEntry{entry}}
	active, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &allocationCancelContext{Context: active, cancel: cancel, polls: 6}
	if _, err := a.Forks(ctx); !errors.Is(err, context.Canceled) { t.Fatal("fallback lookup ignored cancellation", err) }
	if len(published) != publishedCount || published[publishedCount-1].Values[1] != values[1] { t.Fatal("canceled lookup changed retained storage") }
}
