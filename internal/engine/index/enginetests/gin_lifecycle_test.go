package enginetests_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func ginInstalledRoot(t *testing.T, e *core.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	idx := gistCopyRelation(t, e, name)
	e.Mu.Lock()
	root := e.Storage.IndexRoots[idx.Address()]
	store := e.Storage.Relations[idx.SQLIndex.Parent]
	e.Mu.Unlock()
	if root == nil || root.Gin == nil || len(root.Gin.Columns) != len(idx.SQLIndex.Keys) {
		t.Fatal("missing installed GIN payload", name, root)
	}
	ginAssertOwners(t, root, store)
	return idx, root
}
func ginTemporaryIndex(t *testing.T, e *core.Engine, s *session.Session, relation string) *catalog.Relation {
	t.Helper()
	parent, ok := e.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: relation})
	if !ok {
		t.Fatal("missing temporary relation", relation)
	}
	var found *catalog.Relation
	for _, descriptor := range e.CatalogRead().Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == parent {
			if found != nil {
				t.Fatal("ambiguous temporary index", relation)
			}
			found = idx
		}
	}
	if found == nil {
		t.Fatal("missing temporary index", relation)
	}
	return found
}
func ginAssertOwners(t *testing.T, root *storage.IndexRoot, store *storage.Relation) {
	t.Helper()
	members := map[storage.GinPosting]bool{}
	for _, entry := range root.Entries {
		if entry.Position < 0 || entry.Position >= len(store.Versions) {
			t.Fatal("out of range entry", entry)
		}
		row := store.Versions[entry.Position]
		if row.Row != entry.Row || row.Version != entry.Version {
			t.Fatal("entry lacks destination heap ownership", entry, row)
		}
		members[storage.GinPosting{Row: entry.Row, Version: entry.Version, Position: entry.Position}] = true
		for i, key := range entry.Keys {
			if key.Type() != root.Gin.Columns[i].Binding.Usage.Type {
				t.Fatal("source key descriptor retained")
			}
		}
	}
	for _, columns := range [][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
		for _, column := range columns {
			for _, p := range column.NonNull {
				if !members[p] {
					t.Fatal("nonnull membership not owned", p)
				}
			}
			for _, list := range column.Tokens {
				if len(list.Postings) == 0 {
					t.Fatal("empty posting list")
				}
				seen := map[storage.GinPosting]bool{}
				for _, p := range list.Postings {
					if !members[p] || seen[p] {
						t.Fatal("foreign or duplicate posting", p)
					}
					seen[p] = true
				}
				if list.Token.Kind == sqltype.GinOrdinary && column.Binding.Class.InputOID() == 2277 && list.Token.Element.Type() != column.Binding.Usage.Type.BaseType().Element() {
					t.Fatal("source element generation retained")
				}
			}
		}
	}
}
func ginPostingID(t *testing.T, e *core.Engine, idx *catalog.Relation, postings []storage.GinPosting) []int64 {
	t.Helper()
	store := e.Storage.Relations[idx.SQLIndex.Parent]
	var out []int64
	for _, p := range postings {
		n, err := store.Versions[p.Position].Values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func TestGINPublication(t *testing.T) {
	f := newGinFixture(t)
	unchanged, err := index.MergeGinIndexRoot(f.evaluation, f.parent, f.descriptor, f.root, &index.RowPublication{SingleSlot: -1}, true, true)
	if err != nil || unchanged != nil {
		t.Fatal("no-change returned a candidate", unchanged, err)
	}
	e := engine.New()
	for _, q := range []string{"CREATE TABLE gin_publication(id integer CHECK(id>0),a integer[],v tsvector)", "INSERT INTO gin_publication VALUES(1,'{1,2,2}','cat:1'),(2,'{2}','dog:1')", "CREATE INDEX gin_publication_i ON gin_publication USING gin(a,v)"} {
		enginetest.Run(t, e, q)
	}
	idx, old := ginInstalledRoot(t, e, "gin_publication_i")
	oldPayload := ginCopyPayload(old.Gin)
	oldEntries := gistCopyEntries(old)
	dog := ginLookup(t, old.Gin.Columns[1], sqltype.GinToken{Lexeme: "dog"})
	enginetest.Run(t, e, "INSERT INTO gin_publication VALUES(3,'{9,9}','lion:1')")
	_, inserted := ginInstalledRoot(t, e, "gin_publication_i")
	nextDog := ginLookup(t, inserted.Gin.Columns[1], sqltype.GinToken{Lexeme: "dog"})
	if inserted == old || &dog[0] != &nextDog[0] || inserted.Construction != old.Construction {
		t.Fatal("batch delta rebuilt unaffected postings/provenance")
	}
	enginetest.Run(t, e, "UPDATE gin_publication SET a='{5}',v='dog:1' WHERE id=1")
	_, updated := ginInstalledRoot(t, e, "gin_publication_i")
	if got := ginLookup(t, updated.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"}); len(got) != 0 {
		t.Fatal("ended exact predecessor retained", got)
	}
	postings, probeErr := index.GinLexemeCandidates(context.Background(), updated.Gin, 1, "dog", false, false, sqltype.InputEnvironment{})
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	if got := ginPostingID(t, e, idx, postings); !slices.Equal(got, []int64{1, 2}) {
		t.Fatal("actual update postings", got)
	}
	_, err = enginetest.Execute(e, context.Background(), enginetest.Statement(t, "INSERT INTO gin_publication VALUES(4,'{4}','failed:1'),(-1,'{4}','failed:1')"), nil)
	if err == nil {
		t.Fatal("failure fixture was not rejected")
	}
	if _, same := ginInstalledRoot(t, e, "gin_publication_i"); same != updated {
		t.Fatal("failed later row published earlier postings")
	}
	enginetest.Run(t, e, "ALTER INDEX gin_publication_i SET(fastupdate=off,gin_pending_list_limit='0100')")
	configured, same := ginInstalledRoot(t, e, "gin_publication_i")
	if configured == idx || same != updated || configured.StorageOptions.Base[1].Integer != 64 || configured.StorageOptions.Base[1].Text != "0100" {
		t.Fatal("option-only COW rebuilt postings or lost typed lexical intent")
	}
	enginetest.Run(t, e, "ALTER INDEX gin_publication_i RESET(fastupdate,gin_pending_list_limit)")
	if _, same := ginInstalledRoot(t, e, "gin_publication_i"); same != updated {
		t.Fatal("RESET rebuilt posting root")
	}
	if !reflect.DeepEqual(old.Gin, oldPayload) || !reflect.DeepEqual(old.Entries, oldEntries) {
		t.Fatal("publication mutated retained root")
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "INSERT INTO gin_publication VALUES(5,'{5}','private:1')")
	enginetest.SessionSQL(t, s, "ROLLBACK")
	if _, same := ginInstalledRoot(t, e, "gin_publication_i"); same != updated {
		t.Fatal("rollback published private postings")
	}
	enginetest.SessionSQL(t, s, "CREATE TABLE gin_private(id integer,v tsvector)")
	enginetest.SessionSQL(t, s, "INSERT INTO gin_private VALUES(1,'one:1'),(2,NULL),(3,'one:1')")
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "CREATE INDEX gin_private_i ON gin_private USING gin(v)")
	view := e.PrivateCatalogLocked(s.Transactions.Catalog)
	ref := enginetest.CatalogRef(t, e, view, "gin_private_i")
	private := index.JournalIndexesOrZero(s.Transactions.Catalog).UniqueRoots[ref.Address]
	if private == nil || private.Gin == nil || len(private.Entries) != 3 || e.Storage.IndexRoots[ref.Address] != nil {
		t.Fatal("private build escaped or lacks inverted data")
	}
	captured := ginCopyPayload(private.Gin)
	enginetest.SessionSQL(t, s, "SAVEPOINT gin_prefix")
	enginetest.SessionSQL(t, s, "UPDATE gin_private SET v='discarded:1' WHERE id=1")
	enginetest.SessionSQL(t, s, "ROLLBACK TO gin_prefix")
	enginetest.SessionSQL(t, s, "INSERT INTO gin_private VALUES(4,'four:1')")
	enginetest.TransactionEnd(t, s, tx, true)
	_, committed := ginInstalledRoot(t, e, "gin_private_i")
	four, probeErr := index.GinLexemeCandidates(context.Background(), committed.Gin, 0, "four", false, false, sqltype.InputEnvironment{})
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	if committed == private || !reflect.DeepEqual(private.Gin, captured) || len(ginLookup(t, committed.Gin.Columns[0], sqltype.GinToken{Lexeme: "discarded"})) != 0 || len(four) != 1 {
		t.Fatal("private commit/savepoint ownership")
	}
}

func TestGINVacuum(t *testing.T) {
	f := newGinFixture(t)
	before := ginCopyPayload(f.root.Gin)
	entries := gistCopyEntries(f.root)
	positions := []int{0, -1, 1, 2, 3, 4, 5, 6}
	versions := append(slices.Clone(f.store.Versions[:1]), f.store.Versions[2:]...)
	candidate := storage.VacuumCandidate{Versions: versions, Positions: positions, Removed: 1}
	destination := &storage.Relation{Versions: versions}
	next, err := storage.RemapVacuumIndex(context.Background(), f.root, f.store.Versions, candidate, destination, 2)
	if err != nil {
		t.Fatal(err)
	}
	ginAssertOwners(t, next, destination)
	ginWantRows(t, ginLookup(t, next.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"}), 1, 5)
	if next.Construction != f.root.Construction || !reflect.DeepEqual(f.root.Gin, before) || !reflect.DeepEqual(f.root.Entries, entries) {
		t.Fatal("vacuum mutated source/provenance")
	}
	unchanged, err := storage.RemapVacuumIndex(context.Background(), f.root, f.store.Versions, storage.VacuumCandidate{Versions: f.store.Versions}, f.store, 1)
	if err != nil || unchanged != f.root {
		t.Fatal("no compaction replaced root", err)
	}
	invalid := *f.root
	invalid.Gin = ginCopyPayload(f.root.Gin)
	invalid.Gin.Columns[0].Tokens[0].Postings[0].Version = 999
	if candidate, err := storage.RemapVacuumIndex(context.Background(), &invalid, f.store.Versions, candidate, destination, 2); candidate != nil || err == nil {
		t.Fatal("invalid posting identity accepted")
	}
	e := engine.New()
	for _, q := range []string{"CREATE TABLE gin_vacuum(id integer,v tsvector)", "INSERT INTO gin_vacuum VALUES(1,'cat:1'),(2,'cat:1'),(3,'dog:1')", "CREATE INDEX gin_vacuum_i ON gin_vacuum USING gin(v)", "DELETE FROM gin_vacuum WHERE id=2"} {
		enginetest.Run(t, e, q)
	}
	_, prior := ginInstalledRoot(t, e, "gin_vacuum_i")
	priorCopy := ginCopyPayload(prior.Gin)
	enginetest.Run(t, e, "VACUUM gin_vacuum")
	_, installed := ginInstalledRoot(t, e, "gin_vacuum_i")
	if installed == prior || len(installed.Entries) != 2 || !reflect.DeepEqual(prior.Gin, priorCopy) {
		t.Fatal("actual vacuum did not compact immutable postings")
	}
}

func TestGINTransport(t *testing.T) {
	t.Run("structural-transports", ginStructuralTransports)
	t.Run("destination-remint", ginDestinationRemint)
	t.Run("ordinary-rebuilds", ginOrdinaryRebuilds)
}

func ginStructuralTransports(t *testing.T) {
	f := newGinFixture(t)
	before := ginCopyPayload(f.root.Gin)
	history := f.root.Construction
	positions := map[index.RowIdentity]int{{Row: 1, Version: 1}: 0, {Row: 5, Version: 5}: 1}
	baseline := &storage.Relation{Versions: []storage.RowVersion{f.store.Versions[0], f.store.Versions[4]}}
	build := &index.AlterIndexBuild{Index: f.descriptor, Root: f.root, Store: f.store}
	filtered, err := build.BaselineRoot(f.evaluation, f.descriptor, baseline, positions)
	if err != nil {
		t.Fatal(err)
	}
	ginAssertOwners(t, filtered, baseline)
	ginWantRows(t, ginLookup(t, filtered.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"}), 1, 5)
	if filtered.Construction != f.root.Construction {
		t.Fatal("baseline laundered construction")
	}
	state := catalog.CloneState(f.view.State())
	state.SetDescriptor(f.parent.Address(), f.parent)
	state.SetDescriptor(f.descriptor.Address(), f.descriptor)
	view := catalog.NewRead(f.view.Owner(), state)
	stores := map[ids.ObjectAddress]*storage.Relation{f.parent.Address(): f.store}
	roots := map[ids.ObjectAddress]*storage.IndexRoot{f.descriptor.Address(): f.root}
	snapshot, err := index.CaptureSnapshot(context.Background(), index.SnapshotCapture{View: view, Authority: state, Relations: stores, Roots: roots, Frontier: 1, Store: func(a ids.ObjectAddress) *storage.Relation { return stores[a] }, Root: func(a ids.ObjectAddress) *storage.IndexRoot { return roots[a] }})
	if err != nil {
		t.Fatal(err)
	}
	destination := &storage.Relation{Versions: slices.Clone(f.store.Versions)}
	selected := make(map[index.RowIdentity]int)
	for i := range destination.Versions {
		row := &destination.Versions[i]
		selected[index.RowIdentity{Row: row.Row, Version: row.Version}] = i
		row.Row = ids.RowID(100 + i)
		row.Version = ids.VersionID(200 + i)
	}
	transported, err := snapshot.Transport(context.Background(), view, map[ids.ObjectAddress]*storage.Relation{f.parent.Address(): destination}, map[ids.ObjectAddress]map[index.RowIdentity]int{f.parent.Address(): selected})
	if err != nil {
		t.Fatal(err)
	}
	moved := transported[f.descriptor.Address()]
	ginAssertOwners(t, moved, destination)
	ginWantRows(t, ginLookup(t, moved.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"}), 100, 101, 104)
	if moved.Gin == f.root.Gin || moved.Construction == history || !index.ConstructionMatches(moved, f.descriptor) || f.root.Construction != history || !index.ConstructionMatches(f.root, f.descriptor) {
		t.Fatal("snapshot retained source physical root or lost construction lineage")
	}
	// Slot-only layout must not re-extract unchanged typed payloads.
	targetParent := *f.parent
	integer, _ := view.LookupType(sqltype.Int4)
	targetParent.Attributes = []int32{9, 1, 2}
	targetParent.Columns = append([]catalog.Column{{Name: "padding", Usage: integer.Usage()}}, f.parent.Columns...)
	targetStore := &storage.Relation{Versions: slices.Clone(f.store.Versions)}
	targetIndex := *f.descriptor
	for i := range targetStore.Versions {
		targetStore.Versions[i].Values = append([]sqltype.Value{sqltype.Null(integer)}, f.store.Versions[i].Values...)
	}
	journal := &session.Journal{}
	_, _, err = index.TransportCapturedIndexLayouts(f.evaluation, journal, []index.LayoutCapture{{Lineage: index.LayoutLineage{Source: f.descriptor, Target: &targetIndex, SourceParent: f.parent, TargetParent: &targetParent}, Root: f.root}}, f.store, targetStore, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	layout := index.JournalIndexesOrZero(journal).UniqueRoots[targetIndex.Address()]
	if layout == nil || layout.Gin != f.root.Gin || layout.Construction == history || !index.ConstructionMatches(layout, &targetIndex) || f.root.Construction != history {
		t.Fatal("slot-only layout rebuilt immutable postings or lost construction lineage")
	}
	emptyJournal := &session.Journal{}
	if err := index.TransportEmptyIndexStorage(context.Background(), view, emptyJournal, nil, f.parent.Address(), &storage.Relation{}); err != nil {
		t.Fatal(err)
	}
	empty := index.JournalIndexesOrZero(emptyJournal).UniqueRoots[f.descriptor.Address()]
	if empty == nil || empty.Gin == nil || len(empty.Gin.Columns) != 2 || len(empty.Gin.Columns[0].Tokens) != 0 {
		t.Fatal("empty layout discarded column binding")
	}
	if !reflect.DeepEqual(f.root.Gin, before) {
		t.Fatal("transport mutated predecessor")
	}
}

func ginDestinationRemint(t *testing.T) {
	source := engine.New()
	for _, q := range []string{"CREATE SCHEMA gin_padding", "CREATE SCHEMA gin_toast_padding", "CREATE TABLE gin_restore(id integer,v tsvector)", "CREATE DOMAIN gin_restore_key AS tsvector", "ALTER TABLE gin_restore ALTER COLUMN v TYPE gin_restore_key", "INSERT INTO gin_restore VALUES(1,'cat:1'),(2,'removed:1'),(3,NULL)", "CREATE INDEX gin_restore_i ON gin_restore USING gin(v) WITH(fastupdate=off,gin_pending_list_limit=64)", "DELETE FROM gin_restore WHERE id=2", "CREATE TABLE gin_restore_jsonb(id integer,j jsonb)", "INSERT INTO gin_restore_jsonb VALUES(1,'{\"a\":\"b\",\"n\":1}')", "CREATE INDEX gin_restore_jsonb_i ON gin_restore_jsonb USING gin(j)", "CREATE INDEX gin_restore_jsonb_p ON gin_restore_jsonb USING gin(j jsonb_path_ops)"} {
		enginetest.Run(t, source, q)
	}
	parent := gistCopyRelation(t, source, "gin_restore")
	sourceIndex, original := ginInstalledRoot(t, source, "gin_restore_i")
	before := ginCopyPayload(original.Gin)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	temporary := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, temporary, "CREATE TEMP TABLE gin_retained(id integer,v tsvector)")
	retained, ok := target.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: temporary.TemporaryNamespace, Name: "gin_retained"})
	if !ok || retained != parent.Address() {
		t.Fatal("fixture lacks actual destination TEMP collision", retained, parent.Address())
	}
	enginetest.SessionSQL(t, temporary, "CREATE INDEX gin_retained_i ON gin_retained USING gin(v)")
	enginetest.SessionSQL(t, temporary, "INSERT INTO gin_retained VALUES(73,'retained:1')")
	enginetest.SessionSQL(t, temporary, "CREATE TEMP TABLE gin_retained_jsonb(id integer,j jsonb)")
	enginetest.SessionSQL(t, temporary, "CREATE INDEX gin_retained_jsonb_i ON gin_retained_jsonb USING gin(j)")
	enginetest.SessionSQL(t, temporary, "INSERT INTO gin_retained_jsonb VALUES(73,'{\"a\":\"b\"}')")
	enginetest.SessionSQL(t, temporary, "CREATE TEMP TABLE gin_retained_path(id integer,j jsonb)")
	enginetest.SessionSQL(t, temporary, "CREATE INDEX gin_retained_path_i ON gin_retained_path USING gin(j jsonb_path_ops)")
	enginetest.SessionSQL(t, temporary, "INSERT INTO gin_retained_path VALUES(73,'{\"a\":\"b\"}')")
	var retainedIndex *catalog.Relation
	for _, d := range target.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.SQLIndex != nil && r.SQLIndex.Parent == retained {
			retainedIndex = r
		}
	}
	if retainedIndex == nil {
		t.Fatal("missing retained GIN")
	}
	retainedRoot := target.Storage.IndexRoots[retainedIndex.Address()]
	retainedCopy := ginCopyPayload(retainedRoot.Gin)
	opsIndex := ginTemporaryIndex(t, target, temporary, "gin_retained_jsonb")
	pathIndex := ginTemporaryIndex(t, target, temporary, "gin_retained_path")
	opsRoot := target.Storage.IndexRoots[opsIndex.Address()]
	pathRoot := target.Storage.IndexRoots[pathIndex.Address()]
	if opsRoot == nil || opsRoot.Gin == nil || pathRoot == nil || pathRoot.Gin == nil {
		t.Fatal("missing retained jsonb GIN roots")
	}
	opsBinding, pathBinding := opsRoot.Gin.Columns[0].Binding, pathRoot.Gin.Columns[0].Binding
	if !opsBinding.IsJSONB() || opsBinding.JSONBPathOps() || !pathBinding.IsJSONB() || !pathBinding.JSONBPathOps() {
		t.Fatal("retained jsonb GIN bindings", opsBinding, pathBinding)
	}
	opsCopy, pathCopy := ginCopyPayload(opsRoot.Gin), ginCopyPayload(pathRoot.Gin)
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	destination := gistCopyRelation(t, target, "gin_restore")
	idx, next := ginInstalledRoot(t, target, "gin_restore_i")
	if destination.Address() == parent.Address() || next == original || next.Gin == original.Gin || destination.Columns[1].Usage.Type == parent.Columns[1].Usage.Type || !reflect.DeepEqual(idx.StorageOptions, sourceIndex.StorageOptions) {
		t.Fatal("replacement retained source owner/configuration")
	}
	for _, column := range next.Gin.Columns {
		if column.Binding.Usage.Type != destination.Columns[1].Usage.Type {
			t.Fatal("source token provider retained")
		}
	}
	if target.Storage.IndexRoots[retainedIndex.Address()] != retainedRoot || !reflect.DeepEqual(retainedRoot.Gin, retainedCopy) {
		t.Fatal("TEMP policy changed retained root")
	}
	if ginTemporaryIndex(t, target, temporary, "gin_retained_jsonb").Address() != opsIndex.Address() || ginTemporaryIndex(t, target, temporary, "gin_retained_path").Address() != pathIndex.Address() {
		t.Fatal("replacement replaced retained jsonb descriptors")
	}
	if target.Storage.IndexRoots[opsIndex.Address()] != opsRoot || !reflect.DeepEqual(opsRoot.Gin, opsCopy) || target.Storage.IndexRoots[pathIndex.Address()] != pathRoot || !reflect.DeepEqual(pathRoot.Gin, pathCopy) {
		t.Fatal("replacement changed retained jsonb roots")
	}
	if binding := opsRoot.Gin.Columns[0].Binding; !binding.IsJSONB() || binding.JSONBPathOps() {
		t.Fatal("replacement changed retained jsonb_ops binding", binding)
	}
	if binding := pathRoot.Gin.Columns[0].Binding; !binding.IsJSONB() || !binding.JSONBPathOps() {
		t.Fatal("replacement changed retained jsonb_path_ops binding", binding)
	}
	// Deferred placement is genuine, so probe both portions for the maintained
	// row rather than assuming the repaired destination flushed to main.
	enginetest.SessionSQL(t, temporary, "INSERT INTO gin_retained_jsonb VALUES(74,'{\"a\":\"z\"}')")
	maintainedIndex := ginTemporaryIndex(t, target, temporary, "gin_retained_jsonb")
	maintainedJSONB := target.Storage.IndexRoots[maintainedIndex.Address()]
	if maintainedJSONB == nil || maintainedJSONB.Gin == nil {
		t.Fatal("retained jsonb GIN lost its installed root")
	}
	jsonbRows := ginPostingID(t, target, maintainedIndex, ginLookup(t, maintainedJSONB.Gin.Columns[0], sqltype.GinToken{Lexeme: "\x01a"}))
	if len(maintainedJSONB.Gin.Pending) != 0 {
		jsonbRows = append(jsonbRows, ginPostingID(t, target, maintainedIndex, ginLookup(t, maintainedJSONB.Gin.Pending[0], sqltype.GinToken{Lexeme: "\x01a"}))...)
	}
	slices.Sort(jsonbRows)
	if !slices.Equal(jsonbRows, []int64{73, 74}) {
		t.Fatal("post-restore maintenance lost retained jsonb postings", jsonbRows)
	}
	prior := ginCopyPayload(next.Gin)
	enginetest.Run(t, target, "INSERT INTO gin_restore VALUES(4,'cat:1')")
	enginetest.Run(t, target, "UPDATE gin_restore SET v='dog:1' WHERE id=1")
	idx, maintained := ginInstalledRoot(t, target, "gin_restore_i")
	if got := ginPostingID(t, target, idx, ginLookup(t, maintained.Gin.Columns[0], sqltype.GinToken{Lexeme: "cat"})); !slices.Equal(got, []int64{4}) {
		t.Fatal("destination postings not maintained", got)
	}
	jsonbParent := gistCopyRelation(t, target, "gin_restore_jsonb")
	opsRestoredIndex, opsRestored := ginInstalledRoot(t, target, "gin_restore_jsonb_i")
	if opsRestoredIndex.SQLIndex.Parent != jsonbParent.Address() {
		t.Fatal("restored jsonb_ops index bound to a foreign parent")
	}
	opsColumn := opsRestored.Gin.Columns[0]
	if !opsColumn.Binding.IsJSONB() || opsColumn.Binding.JSONBPathOps() || opsColumn.Binding.Usage.Type != jsonbParent.Columns[1].Usage.Type {
		t.Fatal("restored jsonb_ops binding", opsColumn.Binding)
	}
	if got := ginPostingID(t, target, opsRestoredIndex, ginLookup(t, opsColumn, sqltype.GinToken{Lexeme: "\x01a"})); !slices.Equal(got, []int64{1}) {
		t.Fatal("restored jsonb_ops postings", got)
	}
	pathRestoredIndex, pathRestored := ginInstalledRoot(t, target, "gin_restore_jsonb_p")
	if pathRestoredIndex.SQLIndex.Parent != jsonbParent.Address() {
		t.Fatal("restored jsonb_path_ops index bound to a foreign parent")
	}
	pathColumn := pathRestored.Gin.Columns[0]
	if !pathColumn.Binding.IsJSONB() || !pathColumn.Binding.JSONBPathOps() || pathColumn.Binding.Usage.Type != jsonbParent.Columns[1].Usage.Type {
		t.Fatal("restored jsonb_path_ops binding", pathColumn.Binding)
	}
	pathTokens := 0
	for _, list := range pathColumn.Tokens {
		if list.Token.Kind != sqltype.GinOrdinary {
			continue
		}
		if len(list.Token.Lexeme) != 4 {
			t.Fatal("restored jsonb_path_ops token width", list.Token.Lexeme)
		}
		pathTokens++
	}
	if pathTokens == 0 {
		t.Fatal("restored jsonb_path_ops index has no path dictionary")
	}
	if !reflect.DeepEqual(original.Gin, before) || !reflect.DeepEqual(next.Gin, prior) || !reflect.DeepEqual(retainedRoot.Gin, retainedCopy) {
		t.Fatal("destination writes mutated source/predecessor/TEMP postings")
	}
	var after bytes.Buffer
	if err := engine.Dump(context.Background(), source, &after); err != nil || after.String() != dump.String() {
		t.Fatal("destination replacement changed source", err)
	}
}

func ginOrdinaryRebuilds(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE gin_rebuild(id integer,a integer[])", "INSERT INTO gin_rebuild VALUES(1,'{1,2}'),(2,'{2,3}')", "CREATE INDEX gin_rebuild_i ON gin_rebuild USING gin((coalesce(a,a))) WHERE id>0"} {
		enginetest.Run(t, e, q)
	}
	oldIndex, old := ginInstalledRoot(t, e, "gin_rebuild_i")
	before := ginCopyPayload(old.Gin)
	if oldIndex.SQLIndex.Keys[0].Derived == nil || len(old.Membership) != 2 {
		t.Fatal("fixture did not build a genuine partial expression index")
	}
	enginetest.Run(t, e, "ALTER TABLE gin_rebuild ADD COLUMN padding text")
	_, layout := ginInstalledRoot(t, e, "gin_rebuild_i")
	if layout.Gin != old.Gin {
		t.Fatal("unreferenced layout copied posting backing")
	}
	enginetest.Run(t, e, "ALTER TABLE gin_rebuild ALTER COLUMN a TYPE bigint[]")
	_, typed := ginInstalledRoot(t, e, "gin_rebuild_i")
	if typed == layout || typed.Gin.Columns[0].Binding.Usage.Type.BaseType().Element().ID() != sqltype.Int8 {
		t.Fatal("TYPE failed to reconstruct concrete provider")
	}
	enginetest.Run(t, e, "REINDEX INDEX gin_rebuild_i")
	_, rebuilt := ginInstalledRoot(t, e, "gin_rebuild_i")
	if rebuilt == typed || rebuilt.Construction == typed.Construction || !reflect.DeepEqual(old.Gin, before) {
		t.Fatal("REINDEX did not rebuild immutable destination")
	}
	enginetest.Run(t, e, "CREATE TABLE gin_like(LIKE gin_rebuild INCLUDING INDEXES)")
	copyParent := gistCopyRelation(t, e, "gin_like")
	var copyIndex *catalog.Relation
	for _, d := range e.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.SQLIndex != nil && r.SQLIndex.Parent == copyParent.Address() {
			copyIndex = r
		}
	}
	if copyIndex == nil {
		t.Fatal("LIKE omitted index")
	}
	_, empty := ginInstalledRoot(t, e, copyIndex.Name())
	if len(empty.Entries) != 0 || empty.Gin == nil {
		t.Fatal("LIKE missing bound empty root")
	}
	enginetest.Run(t, e, "INSERT INTO gin_like(id,a) VALUES(9,'{9,9}')")
	_, populated := ginInstalledRoot(t, e, copyIndex.Name())
	if populated == empty || populated.Gin == rebuilt.Gin || len(populated.Entries) != 1 {
		t.Fatal("LIKE destination retained source payload")
	}
	// The existing hierarchy owner excludes parent-level GIN indexes. Exercise
	// supported physical leaf indexing and partition-routed writes instead.
	for _, q := range []string{"CREATE TABLE gin_partition(id integer,a integer[]) PARTITION BY RANGE(id)", "CREATE TABLE gin_partition_leaf PARTITION OF gin_partition FOR VALUES FROM(0) TO(10)", "CREATE INDEX gin_partition_i ON gin_partition_leaf USING gin(a)", "INSERT INTO gin_partition VALUES(1,'{1,1}'),(2,'{2}')"} {
		enginetest.Run(t, e, q)
	}
	leaf := gistCopyRelation(t, e, "gin_partition_leaf")
	var child *catalog.Relation
	for _, d := range e.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.SQLIndex != nil && r.SQLIndex.Parent == leaf.Address() {
			child = r
		}
	}
	if child == nil {
		t.Fatal("partition leaf omitted its physical GIN index")
	}
	_, childRoot := ginInstalledRoot(t, e, child.Name())
	if len(childRoot.Entries) != 2 || childRoot.Gin == nil {
		t.Fatal("partition-routed writes omitted leaf postings")
	}
	leafBefore := ginCopyPayload(childRoot.Gin)
	if len(childRoot.Gin.Pending) != 1 || len(childRoot.Gin.Columns[0].Tokens) != 0 || storage.GinPendingIncidences(childRoot.Gin) != 2 {
		t.Fatal("partition-routed writes did not occupy genuine deduplicated pending storage")
	}
	element := childRoot.Gin.Columns[0].Binding.Usage.Type.BaseType().Element()
	one := sqltype.GinToken{Element: ginInput(t, element, "1")}
	postings, err := index.GinArrayCandidates(context.Background(), childRoot.Gin, 0, ginInput(t, childRoot.Gin.Columns[0].Binding.Usage.Type, "{1}"), 2, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ginPostingID(t, e, child, postings); !slices.Equal(got, []int64{1}) {
		t.Fatal("partition-routed duplicate element did not yield one posting", got)
	}
	enginetest.Run(t, e, "UPDATE gin_partition SET a='{3,3}' WHERE id=1")
	enginetest.Run(t, e, "REINDEX INDEX gin_partition_i")
	_, leafRebuilt := ginInstalledRoot(t, e, child.Name())
	three := sqltype.GinToken{Element: ginInput(t, element, "3")}
	if got := ginPostingID(t, e, child, ginLookup(t, leafRebuilt.Gin.Columns[0], three)); !slices.Equal(got, []int64{1}) {
		t.Fatal("routed update or leaf REINDEX lost destination postings", got)
	}
	if leafRebuilt == childRoot || len(leafRebuilt.Gin.Pending) != 0 || len(ginLookup(t, leafRebuilt.Gin.Columns[0], one)) != 0 || !reflect.DeepEqual(childRoot.Gin, leafBefore) {
		t.Fatal("leaf rebuild retained ended postings or mutated its predecessor")
	}
}
