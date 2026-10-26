package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func gistCopyRelation(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	view := e.CatalogRead()
	a, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing published relation", name)
	}
	return view.MustRelation(a)
}

func gistCopyIndex(t *testing.T, e *core.Engine, parent *catalog.Relation) *catalog.Relation {
	t.Helper()
	var found *catalog.Relation
	for _, descriptor := range e.CatalogRead().Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != parent.Address() {
			continue
		}
		if found != nil || idx.SQLIndex.Method != catalog.GistMethod || !idx.SQLIndex.Ready || !idx.SQLIndex.Valid || !idx.SQLIndex.Live {
			t.Fatal("expected one live genuine GiST index", parent.Name(), idx)
		}
		found = idx
	}
	if found == nil {
		t.Fatal("missing destination index", parent.Name())
	}
	return found
}

// Compare the installed root to destination heap identities and independently
// specified original values, without rebuilding an expected root through index.
func gistCopyMembership(t *testing.T, e *core.Engine, parent, idx *catalog.Relation, want map[int64]*string) *storage.IndexRoot {
	t.Helper()
	e.Mu.Lock()
	root, heap := e.Storage.IndexRoots[idx.Address()], e.Storage.Relations[parent.Address()]
	e.Mu.Unlock()
	if root == nil || root.Hash != nil || root.Brin != nil || heap == nil || len(root.Entries) != len(want) {
		t.Fatal("missing or incorrect installed logical root", root, heap, len(want))
	}
	typ := parent.Columns[1].Usage.Type
	entries := make(map[ids.RowID]storage.IndexEntry, len(want))
	seen := make(map[int64]bool, len(want))
	for position, row := range heap.Versions {
		if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), heap.Ends, nil) {
			continue
		}
		id, err := row.Values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		text, ok := want[id]
		if !ok || seen[id] {
			t.Fatal("unexpected destination heap row", id)
		}
		seen[id] = true
		value := sqltype.Null(typ)
		if text != nil {
			value, err = typ.Input(context.Background(), *text, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(row.Values[1], value) {
			t.Fatal("destination original value", id, row.Values[1], value)
		}
		entries[row.Row] = storage.IndexEntry{Row: row.Row, Version: row.Version, Position: position, Keys: []sqltype.Value{value}}
	}
	if len(seen) != len(want) {
		t.Fatal("missing destination heap rows", seen, want)
	}
	for _, entry := range root.Entries {
		expected, ok := entries[entry.Row]
		if !ok || !reflect.DeepEqual(entry, expected) {
			t.Fatal("destination RowID/VersionID/position/original-value membership", entry, expected)
		}
		delete(entries, entry.Row)
	}
	if len(entries) != 0 {
		t.Fatal("missing installed memberships", entries)
	}
	return root
}

func gistCopyEntries(root *storage.IndexRoot) []storage.IndexEntry {
	entries := append([]storage.IndexEntry(nil), root.Entries...)
	for i := range entries {
		entries[i].Keys = append([]sqltype.Value(nil), entries[i].Keys...)
	}
	return entries
}

func TestGiSTLikeDestinationMembership(t *testing.T) {
	e := engine.New()
	// Composed-fixture SQL drives the actual LIKE staging and Finish paths;
	// SQL outcome acceptance is independently covered in reference/indexes.
	for _, q := range []string{
		"CREATE TABLE gist_copy_source(id integer,v tsvector)",
		"INSERT INTO gist_copy_source VALUES(1,'source:1'),(2,NULL)",
		"CREATE INDEX gist_copy_source_i ON gist_copy_source USING gist(v tsvector_ops(siglen=32)) WITH(buffering=off,fillfactor=80)",
	} {
		enginetest.Run(t, e, q)
	}
	source := gistCopyRelation(t, e, "gist_copy_source")
	si := gistCopyIndex(t, e, source)
	sourceText, copyText, changedText := "source:1", "copy:2", "changed:3"
	sourceWant := map[int64]*string{1: &sourceText, 2: nil}
	sourceRoot := gistCopyMembership(t, e, source, si, sourceWant)
	sourceEntries := gistCopyEntries(sourceRoot)
	enginetest.Run(t, e, "CREATE TABLE gist_copy_destination(LIKE gist_copy_source INCLUDING INDEXES)")
	destination := gistCopyRelation(t, e, "gist_copy_destination")
	di := gistCopyIndex(t, e, destination)
	empty := gistCopyMembership(t, e, destination, di, nil)
	if empty == sourceRoot || di.Address() == si.Address() || di.SQLIndex.Keys[0].ClassOptions != si.SQLIndex.Keys[0].ClassOptions || !reflect.DeepEqual(di.StorageOptions, si.StorageOptions) {
		t.Fatal("LIKE transplanted source root/identity or lost captured options")
	}
	enginetest.Run(t, e, "INSERT INTO gist_copy_destination VALUES(10,'copy:2'),(11,NULL),(12,'copy:2')")
	populated := gistCopyMembership(t, e, destination, di, map[int64]*string{10: &copyText, 11: nil, 12: &copyText})
	before := gistCopyEntries(populated)
	if populated == empty || populated == sourceRoot || len(empty.Entries) != 0 {
		t.Fatal("destination did not install an independent populated root")
	}
	enginetest.Run(t, e, "UPDATE gist_copy_destination SET v='changed:3' WHERE id=10")
	enginetest.Run(t, e, "INSERT INTO gist_copy_destination VALUES(13,'changed:3')")
	next := gistCopyMembership(t, e, destination, di, map[int64]*string{10: &changedText, 11: nil, 12: &copyText, 13: &changedText})
	if next == populated || !reflect.DeepEqual(populated.Entries, before) || !reflect.DeepEqual(sourceRoot.Entries, sourceEntries) || gistCopyMembership(t, e, source, si, sourceWant) != sourceRoot {
		t.Fatal("destination replacement mutated retained or source roots")
	}
	nextEntries := gistCopyEntries(next)
	enginetest.Run(t, e, "UPDATE gist_copy_source SET v='source:4' WHERE id=1")
	enginetest.Run(t, e, "ALTER INDEX gist_copy_source_i SET(buffering=on)")
	sourceChanged := "source:4"
	if gistCopyMembership(t, e, source, si, map[int64]*string{1: &sourceChanged, 2: nil}) == sourceRoot || !reflect.DeepEqual(sourceRoot.Entries, sourceEntries) || gistCopyMembership(t, e, destination, di, map[int64]*string{10: &changedText, 11: nil, 12: &copyText, 13: &changedText}) != next || !reflect.DeepEqual(next.Entries, nextEntries) || !reflect.DeepEqual(gistCopyIndex(t, e, destination).StorageOptions, di.StorageOptions) {
		t.Fatal("source writes/options changed destination or predecessor roots")
	}
}

func TestGiSTPartitionDestinationMembership(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE gist_copy_parent(id integer,v tsvector) PARTITION BY RANGE(id)",
		"CREATE TABLE gist_copy_leaf PARTITION OF gist_copy_parent FOR VALUES FROM(0) TO(10)",
		"INSERT INTO gist_copy_parent VALUES(1,'leaf:1'),(2,NULL),(3,'leaf:1')",
		"CREATE INDEX gist_copy_parent_i ON gist_copy_parent USING gist(v tsvector_ops(siglen=32)) WITH(buffering=off,fillfactor=80)",
	} {
		enginetest.Run(t, e, q)
	}
	parent, leaf := gistCopyRelation(t, e, "gist_copy_parent"), gistCopyRelation(t, e, "gist_copy_leaf")
	pi, li := gistCopyIndex(t, e, parent), gistCopyIndex(t, e, leaf)
	original, changed := "leaf:1", "changed:2"
	root := gistCopyMembership(t, e, leaf, li, map[int64]*string{1: &original, 2: nil, 3: &original})
	before := gistCopyEntries(root)
	e.Mu.Lock()
	parentRoot := e.Storage.IndexRoots[pi.Address()]
	e.Mu.Unlock()
	if root == parentRoot || li.Address() == pi.Address() || li.SQLIndex.PartitionParent != pi.Address() || li.SQLIndex.Keys[0].ClassOptions != pi.SQLIndex.Keys[0].ClassOptions || !reflect.DeepEqual(li.StorageOptions, pi.StorageOptions) {
		t.Fatal("leaf root/descriptor/options are not destination-owned")
	}
	enginetest.Run(t, e, "INSERT INTO gist_copy_parent VALUES(4,'changed:2')")
	inserted := gistCopyMembership(t, e, leaf, li, map[int64]*string{1: &original, 2: nil, 3: &original, 4: &changed})
	insertedEntries := gistCopyEntries(inserted)
	enginetest.Run(t, e, "UPDATE gist_copy_parent SET v='changed:2' WHERE id=1")
	enginetest.Run(t, e, "UPDATE gist_copy_leaf SET v='changed:2' WHERE id=2")
	next := gistCopyMembership(t, e, leaf, li, map[int64]*string{1: &changed, 2: &changed, 3: &original, 4: &changed})
	e.Mu.Lock()
	currentParentRoot := e.Storage.IndexRoots[pi.Address()]
	e.Mu.Unlock()
	if inserted == root || next == inserted || !reflect.DeepEqual(root.Entries, before) || !reflect.DeepEqual(inserted.Entries, insertedEntries) || currentParentRoot != parentRoot {
		t.Fatal("routed/direct leaf writes mutated predecessor or virtual parent roots")
	}
}
