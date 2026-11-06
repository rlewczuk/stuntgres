package core_test

import (
 "reflect"
 "slices"
 "testing"

 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/index"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
)

// Real producers retain exact private storage and construction backing even when
// publication creates locator-only clones of their destination descriptors.
func TestPgPrewarmRewriteLocators(t *testing.T) {
 for _, tc := range []struct { name string; setup []string; rewrite string }{
  {"physical_alter", []string{"CREATE TABLE prewarm_rewrite(k integer)", "INSERT INTO prewarm_rewrite SELECT generate_series(1,1000)"}, "ALTER TABLE prewarm_rewrite ALTER COLUMN k TYPE bigint USING k::bigint"},
  {"typed_alter", []string{"CREATE TYPE prewarm_rewrite_type AS(k integer)", "CREATE TABLE prewarm_rewrite OF prewarm_rewrite_type", "INSERT INTO prewarm_rewrite SELECT generate_series(1,1000)"}, "ALTER TYPE prewarm_rewrite_type ALTER ATTRIBUTE k TYPE bigint CASCADE"},
  {"materialized_refresh", []string{"CREATE TABLE prewarm_rewrite_source(k integer)", "INSERT INTO prewarm_rewrite_source SELECT generate_series(1,1000)", "CREATE MATERIALIZED VIEW prewarm_rewrite AS SELECT k FROM prewarm_rewrite_source"}, "REFRESH MATERIALIZED VIEW prewarm_rewrite"},
 } {
  t.Run(tc.name, func(t *testing.T) {
   e, _, _ := prewarmOwnerEngine(t)
   actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
   for _, query := range tc.setup { enginetest.SessionSQL(t, actor, query) }
   enginetest.SessionSQL(t, actor, "CREATE INDEX prewarm_rewrite_idx ON prewarm_rewrite(k)")
   enginetest.SessionSQL(t, actor, "VACUUM (FREEZE) prewarm_rewrite")
   committedView := e.CatalogRead()
   heapRef := enginetest.CatalogRef(t, e, committedView, "prewarm_rewrite")
   indexRef := enginetest.CatalogRef(t, e, committedView, "prewarm_rewrite_idx")
   oldHeap, oldIndex := bufferUnitNode(t, e, "prewarm_rewrite"), bufferUnitNode(t, e, "prewarm_rewrite_idx")
   e.Mu.Lock()
   originalHeap, originalIndex := committedView.MustRelation(heapRef.Address), committedView.MustRelation(indexRef.Address)
   originalHeapDefinition, originalIndexDefinition := *originalHeap, *originalIndex
   originalStore := e.Storage.Relations[heapRef.Address]
   originalVersions, originalHeaders, originalEnds := originalStore.Versions, originalStore.Headers, originalStore.Ends
   originalRows := slices.Clone(originalVersions)
   originalGeneration, originalAuxiliary := originalStore.Generation, originalStore.Auxiliary
   originalRoot := e.Storage.IndexRoots[indexRef.Address]
   originalConstruction := originalRoot.Construction
   originalEntries := slices.Clone(originalRoot.Entries)
   e.Mu.Unlock()
   warm := func() {
    enginetest.TransactionRows(t, actor, "SELECT pg_prewarm('prewarm_rewrite','buffer','main',0,0)", 1)
    enginetest.TransactionRows(t, actor, "SELECT pg_prewarm('prewarm_rewrite_idx','buffer','main',0,0)", 1)
   }
   warm()
   enginetest.TransactionRows(t, actor, "SELECT pg_prewarm('prewarm_rewrite','buffer','main',1,2)", 2)
   for _, fork := range []string{"fsm", "vm"} {
    enginetest.TransactionRows(t, actor, "SELECT pg_prewarm('prewarm_rewrite','buffer','"+fork+"',0,0)", 1)
   }
   enginetest.SessionSQL(t, actor, "BEGIN")
   defer func() { if actor.TransactionState() != session.TxIdle { enginetest.SessionSQL(t, actor, "ROLLBACK") } }()
   enginetest.SessionSQL(t, actor, tc.rewrite)
   actor.Transactions.Mu.Lock()
   e.Mu.Lock()
   journal := actor.Transactions.Catalog
   view := e.PrivateCatalogLocked(journal)
   destination, destinationIndex := view.MustRelation(heapRef.Address), view.MustRelation(indexRef.Address)
   privateHeapDefinition, privateIndexDefinition := *destination, *destinationIndex
   shape := journal.Storage[heapRef.Address]
   candidate := index.JournalIndexesOrZero(journal).Candidates[indexRef.Address]
   exact := shape.Rewritten && shape.Replacement != nil && shape.Replacement != originalStore && candidate.Index == destinationIndex && candidate.Root != nil && candidate.Storage == shape.Replacement.IndexIdentity() && candidate.Generation == shape.Replacement.Generation && candidate.Ends == shape.Replacement.Ends && candidate.Length == len(shape.Replacement.Versions)
   e.Mu.Unlock()
   actor.Transactions.Mu.Unlock()
   if !exact { t.Fatal("produced root did not capture exact private destination storage", tc.name, shape, candidate) }
   privateVersions, privateHeaders, privateEnds := shape.Replacement.Versions, shape.Replacement.Headers, shape.Replacement.Ends
   privateRows := slices.Clone(privateVersions)
   privateGeneration, privateAuxiliary := shape.Replacement.Generation, shape.Replacement.Auxiliary
   definition, construction := destinationIndex.SQLIndex, candidate.Root.Construction
   if construction == nil || !index.ConstructionMatches(candidate.Root, destinationIndex) { t.Fatal("private root lacks actual destination construction", candidate) }
   // Producer reads can change usage. Snapshot immediately before the explicit
   // calls so even one illicit private index touch is independently visible.
   heapBefore := prewarmOwnerCoordinates(t, e, observer, oldHeap)
   indexBefore := prewarmOwnerCoordinates(t, e, observer, oldIndex)
   for _, coordinate := range [][2]int64{{0,0}, {0,1}, {0,2}, {1,0}, {2,0}} {
    if _, ok := heapBefore[coordinate]; !ok { t.Fatal("private rewrite retired genuine main/auxiliary predecessor coordinate", coordinate, heapBefore) }
   }
   if resident := indexBefore[[2]int64{0,0}]; resident[0] < 1 || resident[0] >= 5 || resident[1] != 0 { t.Fatal("private-index witness needs a genuine unsaturated, unpinned incumbent", indexBefore) }
   warm()
   if got := prewarmOwnerCoordinates(t, e, observer, oldHeap); !reflect.DeepEqual(got, heapBefore) { t.Fatal("private heap changed incumbent coordinates/usage/pins", got, heapBefore) }
   if got := prewarmOwnerCoordinates(t, e, observer, oldIndex); !reflect.DeepEqual(got, indexBefore) { t.Fatal("private index changed incumbent coordinates/usage/pins", got, indexBefore) }
   if !storage.SameVersionRoot(privateVersions, shape.Replacement.Versions) || shape.Replacement.Headers != privateHeaders || shape.Replacement.Ends != privateEnds || shape.Replacement.Generation != privateGeneration || shape.Replacement.Auxiliary != privateAuxiliary || !reflect.DeepEqual(shape.Replacement.Versions, privateRows) { t.Fatal("prewarm mutated private destination roots/headers/forks") }
   enginetest.SessionSQL(t, actor, "COMMIT")
   e.Mu.Lock()
   publishedView := e.Catalog.Current()
   publishedHeap, publishedIndex := publishedView.MustRelation(heapRef.Address), publishedView.MustRelation(indexRef.Address)
   acceptedStore, acceptedRoot := e.Storage.Relations[heapRef.Address], e.Storage.IndexRoots[indexRef.Address]
   newHeap, newIndex := publishedHeap.EffectiveStorageLocator(publishedView), publishedIndex.EffectiveStorageLocator(publishedView)
   e.Mu.Unlock()
   if acceptedStore != shape.Replacement { t.Fatal("commit reconstructed rather than retained accepted destination storage") }
   if publishedIndex.SQLIndex != definition || acceptedRoot == nil || acceptedRoot.Construction != construction || !index.ConstructionMatches(acceptedRoot, publishedIndex) { t.Fatal("commit lost actual accepted definition/construction backing", publishedIndex, acceptedRoot) }
   if len(publishedHeap.Columns) != len(destination.Columns) || len(destination.Columns) == 0 || &publishedHeap.Columns[0] != &destination.Columns[0] || publishedHeap.View != destination.View { t.Fatal("locator publication replaced defining heap backing") }
   // Ignore only the newly published locator, not defining metadata or retained
   // descriptors. The private and predecessor reads remain separate owners.
   heapClone, indexClone := *publishedHeap, *publishedIndex
   heapClone.StorageLocator, indexClone.StorageLocator = destination.StorageLocator, destinationIndex.StorageLocator
   if !reflect.DeepEqual(heapClone, privateHeapDefinition) || !reflect.DeepEqual(indexClone, privateIndexDefinition) || !reflect.DeepEqual(*destination, privateHeapDefinition) || !reflect.DeepEqual(*destinationIndex, privateIndexDefinition) { t.Fatal("publication changed more than locator or mutated retained private descriptors") }
   if !reflect.DeepEqual(*originalHeap, originalHeapDefinition) || !reflect.DeepEqual(*originalIndex, originalIndexDefinition) || !storage.SameVersionRoot(originalVersions, originalStore.Versions) || originalStore.Headers != originalHeaders || originalStore.Ends != originalEnds || originalStore.Generation != originalGeneration || originalStore.Auxiliary != originalAuxiliary || !reflect.DeepEqual(originalStore.Versions, originalRows) || originalRoot.Construction != originalConstruction || !reflect.DeepEqual(originalRoot.Entries, originalEntries) { t.Fatal("publication mutated retained predecessor descriptor/storage/root") }
   if len(acceptedRoot.Entries) != len(acceptedStore.Versions) { t.Fatal("committed B-tree does not cover accepted heap root") }
   for _, entry := range acceptedRoot.Entries {
    if entry.Position < 0 || entry.Position >= len(acceptedStore.Versions) { t.Fatal("committed B-tree entry escaped accepted heap", entry) }
    row := acceptedStore.Versions[entry.Position]
    if entry.Row != row.Row || entry.Version != row.Version || !reflect.DeepEqual(entry.Keys, row.Values) { t.Fatal("committed B-tree entry lost exact heap row/version/key backing", entry, row) }
   }
   if newHeap == 0 || newHeap == oldHeap || newIndex == 0 || newIndex == oldIndex { t.Fatal("commit did not publish fresh heap/index locators", oldHeap, newHeap, oldIndex, newIndex) }
   for _, node := range []ids.OID{oldHeap, oldIndex} {
    if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("commit retained predecessor coordinates", node, got) }
   }
   for _, node := range []ids.OID{newHeap, newIndex} {
    if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("committed destination inherited occupancy", node, got) }
   }
   warm()
   expected := map[[2]int64][2]int64{{0,0}: {1,0}}
   for _, node := range []ids.OID{newHeap, newIndex} {
    if got := prewarmOwnerCoordinates(t, e, observer, node); !reflect.DeepEqual(got, expected) { t.Fatal("new incarnation did not genuinely preload", node, got, expected) }
   }
  })
 }
}
