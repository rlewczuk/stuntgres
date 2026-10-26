package enginetests_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"testing"
)

func spgistCopyIndex(t *testing.T, e *core.Engine, parent *catalog.Relation) *catalog.Relation {
	t.Helper()
	var found *catalog.Relation
	for _, d := range e.CatalogRead().Descriptors() {
		if idx, ok := d.(*catalog.Relation); ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == parent.Address() {
			if found != nil || idx.SQLIndex.Method != catalog.SpgistMethod {
				t.Fatal("wrong destination index", idx)
			}
			found = idx
		}
	}
	if found == nil {
		t.Fatal("missing destination index")
	}
	return found
}

func TestSPGiSTDestinationMembership(t *testing.T) {
	e := engine.New()
	// Composed-fixture SQL drives the actual LIKE staging and Finish paths;
	// SQL outcome acceptance is independently covered in reference/indexes.
	for _, q := range []string{
		"CREATE TABLE spgist_copy_source(id integer,v text)",
		"INSERT INTO spgist_copy_source VALUES(1,'source:1'),(2,NULL)",
		"CREATE INDEX spgist_copy_source_i ON spgist_copy_source USING spgist(v text_ops) WITH(fillfactor=80)",
	} {
		enginetest.Run(t, e, q)
	}
	source := gistCopyRelation(t, e, "spgist_copy_source")
	si := spgistCopyIndex(t, e, source)
	sourceText, copyText, changedText := "source:1", "copy:2", "changed:3"
	sourceWant := map[int64]*string{1: &sourceText, 2: nil}
	sourceRoot := gistCopyMembership(t, e, source, si, sourceWant)
	sourceEntries := gistCopyEntries(sourceRoot)
	enginetest.Run(t, e, "CREATE TABLE spgist_copy_destination(LIKE spgist_copy_source INCLUDING INDEXES)")
	destination := gistCopyRelation(t, e, "spgist_copy_destination")
	di := spgistCopyIndex(t, e, destination)
	empty := gistCopyMembership(t, e, destination, di, nil)
	if empty == sourceRoot || di.Address() == si.Address() || di.SQLIndex.Keys[0].ClassOptions != si.SQLIndex.Keys[0].ClassOptions || !reflect.DeepEqual(di.StorageOptions, si.StorageOptions) {
		t.Fatal("LIKE transplanted source root/identity or lost captured options")
	}
	enginetest.Run(t, e, "INSERT INTO spgist_copy_destination VALUES(10,'copy:2'),(11,NULL),(12,'copy:2')")
	populated := gistCopyMembership(t, e, destination, di, map[int64]*string{10: &copyText, 11: nil, 12: &copyText})
	before := gistCopyEntries(populated)
	if populated == empty || populated == sourceRoot || len(empty.Entries) != 0 {
		t.Fatal("destination did not install an independent populated root")
	}
	enginetest.Run(t, e, "UPDATE spgist_copy_destination SET v='changed:3' WHERE id=10")
	enginetest.Run(t, e, "INSERT INTO spgist_copy_destination VALUES(13,'changed:3')")
	next := gistCopyMembership(t, e, destination, di, map[int64]*string{10: &changedText, 11: nil, 12: &copyText, 13: &changedText})
	if next == populated || !reflect.DeepEqual(populated.Entries, before) || !reflect.DeepEqual(sourceRoot.Entries, sourceEntries) || gistCopyMembership(t, e, source, si, sourceWant) != sourceRoot {
		t.Fatal("destination replacement mutated retained or source roots")
	}
	nextEntries := gistCopyEntries(next)
	enginetest.Run(t, e, "UPDATE spgist_copy_source SET v='source:4' WHERE id=1")
	enginetest.Run(t, e, "ALTER INDEX spgist_copy_source_i SET(fillfactor=90)")
	sourceChanged := "source:4"
	if gistCopyMembership(t, e, source, si, map[int64]*string{1: &sourceChanged, 2: nil}) == sourceRoot || !reflect.DeepEqual(sourceRoot.Entries, sourceEntries) || gistCopyMembership(t, e, destination, di, map[int64]*string{10: &changedText, 11: nil, 12: &copyText, 13: &changedText}) != next || !reflect.DeepEqual(next.Entries, nextEntries) || !reflect.DeepEqual(spgistCopyIndex(t, e, destination).StorageOptions, di.StorageOptions) {
		t.Fatal("source writes/options changed destination or predecessor roots")
	}
}

func TestSPGiSTRestoreMembership(t *testing.T) {
	source := engine.New()
	// Match the two real TEMP namespace allocations, then prove an actual
	// relation-address collision below instead of presuming foreign OIDs differ.
	for _, q := range []string{
		"CREATE SCHEMA spgist_restore_padding",
		"CREATE SCHEMA spgist_restore_toast_padding",
		"CREATE TABLE spgist_restore_rows(id integer,v text)",
		"CREATE DOMAIN spgist_restore_key AS text",
		"ALTER TABLE spgist_restore_rows ALTER COLUMN v TYPE spgist_restore_key",
		"INSERT INTO spgist_restore_rows VALUES(1,'one'),(2,'removed'),(3,NULL),(4,'one')",
		"CREATE INDEX spgist_restore_i ON spgist_restore_rows USING spgist(v text_ops) WITH(fillfactor=80)",
		"UPDATE spgist_restore_rows SET v='changed' WHERE id=1",
		"DELETE FROM spgist_restore_rows WHERE id=2",
	} {
		enginetest.Run(t, source, q)
	}
	parent := gistCopyRelation(t, source, "spgist_restore_rows")
	idx := spgistCopyIndex(t, source, parent)
	one, changed := "one", "changed"
	want := map[int64]*string{1: &changed, 3: nil, 4: &one}
	original := gistCopyMembership(t, source, parent, idx, want)
	originalEntries := gistCopyEntries(original)
	sourceType := parent.Columns[1].Usage.Type
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}

	target := engine.New()
	temporary := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, temporary, "CREATE TEMP TABLE spgist_restore_retained(id integer,v text)")
	view := target.CatalogRead()
	retainedAddress, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: temporary.TemporaryNamespace, Name: "spgist_restore_retained"})
	if !ok || retainedAddress != parent.Address() {
		t.Fatal("fixture did not create a real retained TEMP relation collision", retainedAddress, parent.Address())
	}
	retainedParent := view.MustRelation(retainedAddress)
	enginetest.SessionSQL(t, temporary, "CREATE INDEX spgist_restore_retained_i ON spgist_restore_retained USING spgist(v)")
	enginetest.SessionSQL(t, temporary, "INSERT INTO spgist_restore_retained VALUES(73,'retained')")
	retainedIndex := spgistCopyIndex(t, target, retainedParent)
	retainedText := "retained"
	retainedRoot := gistCopyMembership(t, target, retainedParent, retainedIndex, map[int64]*string{73: &retainedText})
	retainedEntries := gistCopyEntries(retainedRoot)
	retainedDescriptors := make(map[ids.ObjectAddress]catalog.Descriptor)
	for a, d := range target.CatalogRead().Descriptors() {
		if a == temporary.TemporaryNamespace || d.Namespace() == temporary.TemporaryNamespace {
			retainedDescriptors[a] = d
		}
	}
	for _, q := range []string{
		"CREATE TABLE spgist_restore_predecessor(id integer,v text)",
		"INSERT INTO spgist_restore_predecessor VALUES(1,'predecessor')",
		"CREATE INDEX spgist_restore_predecessor_i ON spgist_restore_predecessor USING spgist(v)",
	} {
		enginetest.Run(t, target, q)
	}
	predecessorParent := gistCopyRelation(t, target, "spgist_restore_predecessor")
	predecessorIndex := spgistCopyIndex(t, target, predecessorParent)
	predecessorText := "predecessor"
	predecessor := gistCopyMembership(t, target, predecessorParent, predecessorIndex, map[int64]*string{1: &predecessorText})
	predecessorEntries := gistCopyEntries(predecessor)
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	destination := gistCopyRelation(t, target, "spgist_restore_rows")
	di := spgistCopyIndex(t, target, destination)
	next := gistCopyMembership(t, target, destination, di, want)
	destinationType := destination.Columns[1].Usage.Type
	if destination.Address() == parent.Address() || next == original || next == predecessor || next == retainedRoot || destinationType == sourceType || di.SQLIndex.Keys[0].Class.MethodOID() != 4000 || !reflect.DeepEqual(di.StorageOptions, idx.StorageOptions) {
		t.Fatal("Restore did not install destination-owned reminted typed membership")
	}
	for _, entry := range next.Entries {
		if entry.Keys[0].Type() != destinationType {
			t.Fatal("Restore root retained source domain authority", entry)
		}
	}
	for a, d := range retainedDescriptors {
		if target.CatalogRead().Get(a) != d {
			t.Fatal("Restore changed retained TEMP catalog identity", a)
		}
	}
	retainedRows := enginetest.TransactionRows(t, temporary, "SELECT id FROM pg_temp.spgist_restore_retained", 73)
	if err := retainedRows.Close(); err != nil {
		t.Fatal(err)
	}
	nextEntries := gistCopyEntries(next)
	for _, q := range []string{
		"INSERT INTO spgist_restore_rows VALUES(5,'next')",
		"UPDATE spgist_restore_rows SET v='updated' WHERE id=1",
		"DELETE FROM spgist_restore_rows WHERE id=4",
		"REINDEX INDEX spgist_restore_i",
	} {
		enginetest.Run(t, target, q)
	}
	updated, added := "updated", "next"
	maintained := gistCopyMembership(t, target, destination, di, map[int64]*string{1: &updated, 3: nil, 5: &added})
	if maintained == next || !reflect.DeepEqual(next.Entries, nextEntries) || !reflect.DeepEqual(original.Entries, originalEntries) || !reflect.DeepEqual(predecessor.Entries, predecessorEntries) || !reflect.DeepEqual(retainedRoot.Entries, retainedEntries) || gistCopyMembership(t, target, retainedParent, retainedIndex, map[int64]*string{73: &retainedText}) != retainedRoot || gistCopyMembership(t, source, parent, idx, want) != original {
		t.Fatal("destination maintenance mutated retained source/TEMP/predecessor roots")
	}
	var after bytes.Buffer
	if err := engine.Dump(context.Background(), source, &after); err != nil || after.String() != dump.String() {
		t.Fatal("foreign Restore or destination maintenance changed source", err)
	}
	maintainedEntries := gistCopyEntries(maintained)
	enginetest.Run(t, source, "UPDATE spgist_restore_rows SET v='source-only' WHERE id=1")
	sourceOnly := "source-only"
	if gistCopyMembership(t, source, parent, idx, map[int64]*string{1: &sourceOnly, 3: nil, 4: &one}) == original || !reflect.DeepEqual(original.Entries, originalEntries) || gistCopyMembership(t, target, destination, di, map[int64]*string{1: &updated, 3: nil, 5: &added}) != maintained || !reflect.DeepEqual(maintained.Entries, maintainedEntries) {
		t.Fatal("source maintenance changed destination or predecessor membership")
	}
	enginetest.LockEmpty(t, source)
	enginetest.LockEmpty(t, target)
}

func TestSPGiSTPartitionMembership(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE spgist_copy_parent(id integer,v text) PARTITION BY RANGE(id)",
		"CREATE TABLE spgist_copy_leaf PARTITION OF spgist_copy_parent FOR VALUES FROM(0) TO(10)",
		"INSERT INTO spgist_copy_parent VALUES(1,'leaf:1'),(2,NULL),(3,'leaf:1')",
		"CREATE INDEX spgist_copy_parent_i ON spgist_copy_parent USING spgist(v text_ops) WITH(fillfactor=80)",
	} {
		enginetest.Run(t, e, q)
	}
	parent, leaf := gistCopyRelation(t, e, "spgist_copy_parent"), gistCopyRelation(t, e, "spgist_copy_leaf")
	pi, li := spgistCopyIndex(t, e, parent), spgistCopyIndex(t, e, leaf)
	original, changed := "leaf:1", "changed:2"
	root := gistCopyMembership(t, e, leaf, li, map[int64]*string{1: &original, 2: nil, 3: &original})
	before := gistCopyEntries(root)
	e.Mu.Lock()
	parentRoot := e.Storage.IndexRoots[pi.Address()]
	e.Mu.Unlock()
	if root == parentRoot || li.Address() == pi.Address() || li.SQLIndex.PartitionParent != pi.Address() || li.SQLIndex.Keys[0].ClassOptions != pi.SQLIndex.Keys[0].ClassOptions || !reflect.DeepEqual(li.StorageOptions, pi.StorageOptions) {
		t.Fatal("leaf root/descriptor/options are not destination-owned")
	}
	enginetest.Run(t, e, "INSERT INTO spgist_copy_parent VALUES(4,'changed:2')")
	inserted := gistCopyMembership(t, e, leaf, li, map[int64]*string{1: &original, 2: nil, 3: &original, 4: &changed})
	insertedEntries := gistCopyEntries(inserted)
	enginetest.Run(t, e, "UPDATE spgist_copy_parent SET v='changed:2' WHERE id=1")
	enginetest.Run(t, e, "UPDATE spgist_copy_leaf SET v='changed:2' WHERE id=2")
	next := gistCopyMembership(t, e, leaf, li, map[int64]*string{1: &changed, 2: &changed, 3: &original, 4: &changed})
	e.Mu.Lock()
	currentParentRoot := e.Storage.IndexRoots[pi.Address()]
	e.Mu.Unlock()
	if inserted == root || next == inserted || !reflect.DeepEqual(root.Entries, before) || !reflect.DeepEqual(inserted.Entries, insertedEntries) || currentParentRoot != parentRoot {
		t.Fatal("routed/direct leaf writes mutated predecessor or virtual parent roots")
	}
}

func TestSPGiSTPublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE spgist_publication(id integer,v text)")
	enginetest.SessionSQL(t, s, "INSERT INTO spgist_publication VALUES(1,'one'),(2,NULL),(3,'one')")
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "CREATE INDEX spgist_publication_i ON spgist_publication USING spgist(v)")
	view := e.PrivateCatalogLocked(s.Transactions.Catalog)
	ref := enginetest.CatalogRef(t, e, view, "spgist_publication_i")
	journal := index.JournalIndexesOrZero(s.Transactions.Catalog)
	root := journal.UniqueRoots[ref.Address]
	if root == nil || len(root.Entries) != 3 || e.Storage.IndexRoots[ref.Address] != nil || e.Catalog.State.Get(ref.Address) != nil {
		t.Fatal("private root escaped", root)
	}
	before := gistCopyEntries(root)
	enginetest.SessionSQL(t, s, "SAVEPOINT prefix")
	enginetest.SessionSQL(t, s, "UPDATE spgist_publication SET v='discarded' WHERE id=1")
	enginetest.SessionSQL(t, s, "INSERT INTO spgist_publication VALUES(4,'discarded')")
	enginetest.SessionSQL(t, s, "ROLLBACK TO prefix")
	if !reflect.DeepEqual(root.Entries, before) {
		t.Fatal("savepoint mutated predecessor")
	}
	enginetest.SessionSQL(t, s, "INSERT INTO spgist_publication VALUES(5,'five')")
	enginetest.TransactionEnd(t, s, tx, true)
	parent := gistCopyRelation(t, e, "spgist_publication")
	idx := spgistCopyIndex(t, e, parent)
	one, five := "one", "five"
	final := gistCopyMembership(t, e, parent, idx, map[int64]*string{1: &one, 2: nil, 3: &one, 5: &five})
	if final == root || !reflect.DeepEqual(root.Entries, before) {
		t.Fatal("commit mutated private predecessor")
	}
	enginetest.SessionSQL(t, s, "ALTER INDEX spgist_publication_i SET(fillfactor=80)")
	if e.Storage.IndexRoots[idx.Address()] != final {
		t.Fatal("metadata-only option edit rebuilt root")
	}
	enginetest.SessionSQL(t, s, "ALTER INDEX spgist_publication_i RESET(fillfactor)")
	if e.Storage.IndexRoots[idx.Address()] != final {
		t.Fatal("option RESET rebuilt root")
	}
	enginetest.LockEmpty(t, e)
}
