package core_test

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func analyzeTemporaryRelation(t *testing.T, e *core.Engine, s *session.Session, name string) *catalog.Relation {
	t.Helper()
	view := e.CatalogRead()
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == name && view.MustNamespace(relation.Namespace()).Temporary == s.SessionToken {
			return relation
		}
	}
	t.Fatal("missing actual session TEMP relation", name)
	return nil
}

func TestAnalyzeRetainedTemporaryMetadata(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "self_import", "foreign_collision"} {
		t.Run(replacement, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			// TEMP partition definitions are explicitly unsupported. Exercise
			// genuine admitted TEMP inheritance instead of fabricating a TEMP
			// partition descriptor. Persistent partition metadata below must
			// still be discarded on native replacement, never retained as TEMP.
			for _, q := range []string{
				"CREATE TABLE analyze_persistent(a INT)",
				"INSERT INTO analyze_persistent VALUES(1),(1),(2)",
				"ANALYZE analyze_persistent",
				"CREATE TEMP TABLE analyze_kept_heap(a INT)",
				"INSERT INTO analyze_kept_heap VALUES(3),(3),(4)",
				"CREATE TEMP TABLE analyze_kept_parent(a INT)",
				"CREATE TEMP TABLE analyze_kept_leaf() INHERITS(analyze_kept_parent)",
				"INSERT INTO analyze_kept_leaf VALUES(5),(5),(5),(5)",
				"ANALYZE analyze_kept_heap, analyze_kept_parent",
				"CREATE TABLE analyze_replaced_parent(a INT) PARTITION BY RANGE(a)",
				"CREATE TABLE analyze_replaced_leaf PARTITION OF analyze_replaced_parent FOR VALUES FROM(0) TO(10)",
				"INSERT INTO analyze_replaced_parent VALUES(6),(6)",
				"ANALYZE analyze_replaced_parent",
			} {
				enginetest.SessionSQL(t, s, q)
			}
			persistentRef, persistentStore := vacuumStore(t, e, "analyze_persistent")
			persistentFacts := analyzeFacts(e, persistentRef.Address)
			persistentParent := enginetest.CatalogRef(t, e, e.CatalogRead(), "analyze_replaced_parent")
			if metadata := e.Storage.RelationMetadata[persistentParent.Address]; metadata == nil || metadata.Statistics == nil || metadata.Tuples != 2 || e.Storage.Relations[persistentParent.Address] != nil {
				t.Fatal("persistent partition fixture omitted real metadata-only authority")
			}
			heap := analyzeTemporaryRelation(t, e, s, "analyze_kept_heap")
			parent := analyzeTemporaryRelation(t, e, s, "analyze_kept_parent")
			leaf := analyzeTemporaryRelation(t, e, s, "analyze_kept_leaf")
			heapStore, parentStore, leafStore := e.Storage.Relations[heap.Address()], e.Storage.Relations[parent.Address()], e.Storage.Relations[leaf.Address()]
			if heapStore == nil || parentStore == nil || leafStore == nil {
				t.Fatal("TEMP fixture physical ownership is invalid")
			}
			retained := map[ids.ObjectAddress]*storage.RelationMetadata{}
			for _, relation := range []*catalog.Relation{heap, parent, leaf} {
				metadata := e.Storage.RelationMetadata[relation.Address()]
				if metadata == nil || metadata.Statistics == nil || metadata.AnalyzeCount != 1 || metadata.LastAnalyze.IsZero() {
					t.Fatal("actual TEMP ANALYZE omitted canonical facts", relation.Name())
				}
				retained[relation.Address()] = metadata
			}
			heapMaintenance, parentMaintenance, leafMaintenance := heapStore.Maintenance, parentStore.Maintenance, leafStore.Maintenance
			if !heapMaintenance.Scanned || heapMaintenance.Tuples != 3 || !parentMaintenance.Scanned || parentMaintenance.Tuples != 0 || !retained[parent.Address()].InheritedScanned || retained[parent.Address()].InheritedTuples != 4 {
				t.Fatal("TEMP fixture omitted local heap/inherited cardinality authority")
			}
			before := e.CatalogRead()
			// Failed and preparation-canceled real replacement must preserve all
			// canonical record pointers, not reconstruct equal-looking facts.
			previousMap := maps.Clone(e.Storage.RelationMetadata)
			if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE invalid(")); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			if !maps.Equal(previousMap, e.Storage.RelationMetadata) || e.CatalogRead().State() != before.State() {
				t.Fatal("failed Restore changed canonical metadata")
			}
			ctx, cancel := context.WithCancel(context.Background())
			boundary := &cancelAfterPreflightContext{Context: ctx, cancel: cancel}
			_, err := e.ReplaceCatalog(boundary, e, before)
			cancel()
			if !errors.Is(err, context.Canceled) || boundary.checks < 2 || !maps.Equal(previousMap, e.Storage.RelationMetadata) || e.CatalogRead().State() != before.State() {
				t.Fatal("preparation-canceled replacement changed canonical metadata", err)
			}
			var importedAddress ids.ObjectAddress
			var importedSourceFacts *storage.RelationStatistics
			switch replacement {
			case "reset":
				err = engine.Reset(context.Background(), e)
			case "restore":
				err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE analyze_restored(a INT); INSERT INTO analyze_restored VALUES(9);"))
			case "self_import":
				_, err = e.ReplaceCatalog(context.Background(), e, before)
			case "foreign_collision":
				source := engine.New()
				// Match the destination's first table and two TEMP namespace
				// allocations with real persistent schema allocations. The next
				// imported heap then collides with a genuinely retained TEMP heap.
				for _, q := range []string{"CREATE TABLE analyze_imported_persistent(a INT)", "CREATE SCHEMA analyze_pad", "CREATE SCHEMA analyze_toast_pad", "CREATE TABLE analyze_imported(a INT)", "INSERT INTO analyze_imported VALUES(9),(9)", "ANALYZE analyze_imported"} {
					enginetest.Run(t, source, q)
				}
				imported := enginetest.CatalogRef(t, source, source.CatalogRead(), "analyze_imported")
				importedSourceFacts = analyzeFacts(source, imported.Address)
				importedAddress = imported.Address
				if importedAddress != heap.Address() {
					t.Fatal("fixture did not create an actual retained-TEMP address collision")
				}
				_, err = e.ReplaceCatalog(context.Background(), source, source.CatalogRead())
				if err == nil {
					installed := enginetest.CatalogRef(t, e, e.CatalogRead(), "analyze_imported")
					if installed.Address == importedAddress {
						t.Fatal("import collision was not reminted away from retained TEMP identity")
					}
					importedAddress = installed.Address
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().Epoch() == before.Epoch() {
				t.Fatal("native replacement did not change catalog generation")
			}
			for _, relation := range []*catalog.Relation{heap, parent, leaf} {
				current, exists := e.CatalogRead().Relation(relation.Address())
				metadata := e.Storage.RelationMetadata[relation.Address()]
				if !exists || current != relation || metadata == nil || *metadata != *retained[relation.Address()] {
					t.Fatal("genuinely retained TEMP identity lost typed backing/cardinality/count/time", relation.Name())
				}
				resolved := session.ResolveStatistics(nil, relation.Address(), metadata, current, e.CatalogRead())
				for key, fact := range retained[relation.Address()].Statistics.Columns {
					if resolved == nil || resolved.Columns[key] != fact {
						t.Fatal("retained TEMP statistics rejected compatible live authority", relation.Name(), key)
					}
				}
			}
			if e.Storage.Relations[heap.Address()] != heapStore || e.Storage.Relations[parent.Address()] != parentStore || e.Storage.Relations[leaf.Address()] != leafStore || heapStore.Maintenance != heapMaintenance || parentStore.Maintenance != parentMaintenance || leafStore.Maintenance != leafMaintenance {
				t.Fatal("retained TEMP physical roots or local cardinality changed")
			}
			if replacement == "self_import" {
				if e.Storage.Relations[persistentRef.Address] == persistentStore || analyzeFacts(e, persistentRef.Address) != nil {
					t.Fatal("persistent self-import reused old physical/statistics authority")
				}
			}
			if replacement == "foreign_collision" {
				imported := analyzeFacts(e, importedAddress)
				column := storage.StatisticsKey{Attribute: 1}
				if imported == nil || importedSourceFacts == nil || imported == persistentFacts || imported == importedSourceFacts || len(imported.Columns) != 1 || imported.Columns[column] == nil || imported.Columns[column] == importedSourceFacts.Columns[column] {
					t.Fatal("imported address collision failed to remint only source column facts")
				}
				fact := imported.Columns[column]
				current := e.CatalogRead().MustRelation(importedAddress)
				if fact.Rows != 2 || fact.NullCount != 0 || !fact.DistinctKnown || fact.DistinctCount != 1 || len(fact.MCV) != 1 || fact.MCV[0].Count != 2 || fact.Usage != current.Columns[0].Usage || !fact.MCV[0].Value.ValidResult(fact.Usage) {
					t.Fatal("collision import lost measured counts or destination typed authority")
				}
				enginetest.TransactionRows(t, s, "SELECT a FROM analyze_imported", 9, 9)
			}
			if e.Storage.RelationMetadata[persistentParent.Address] != nil || e.Storage.Relations[persistentParent.Address] != nil {
				t.Fatal("persistent partition metadata survived native replacement")
			}
			enginetest.TransactionRows(t, s, "SELECT a FROM analyze_kept_heap ORDER BY a", 3, 3, 4)
			enginetest.TransactionRows(t, s, "SELECT a FROM analyze_kept_parent ORDER BY a", 5, 5, 5, 5)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestAnalyzeTruncateJournalOrdering(t *testing.T) {
	for _, sequence := range []string{"scan_then_truncate", "truncate_then_empty_scan", "truncate_insert_scan", "savepoint_restores_scanned_truncate", "savepoint_discards_typed_scan", "rewrite_scan_then_truncate", "rewrite_truncate_insert_scan"} {
		for _, prepared := range []bool{false, true} {
			name := sequence + map[bool]string{false: "/commit", true: "/prepared"}[prepared]
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				finisher := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE analyze_truncate(a INT)", "INSERT INTO analyze_truncate VALUES(1),(1),(2)", "ANALYZE analyze_truncate"} {
					enginetest.SessionSQL(t, s, q)
				}
				ref, old := vacuumStore(t, e, "analyze_truncate")
				baseline := e.Storage.RelationMetadata[ref.Address]
				key := storage.StatisticsKey{Attribute: e.CatalogRead().MustRelation(ref.Address).Attributes[0]}
				enginetest.SessionSQL(t, s, "BEGIN")
				rewrite := strings.HasPrefix(sequence, "rewrite_")
				if rewrite {
					enginetest.SessionSQL(t, s, "ALTER TABLE analyze_truncate ALTER COLUMN a TYPE BIGINT")
				}
				wantRows := []int64{}
				wantScanned, wantTuples := true, int64(0)
				if sequence == "scan_then_truncate" || sequence == "rewrite_scan_then_truncate" {
					enginetest.SessionSQL(t, s, "ANALYZE analyze_truncate")
					enginetest.SessionSQL(t, s, "TRUNCATE analyze_truncate")
					wantScanned = false
				} else {
					enginetest.SessionSQL(t, s, "TRUNCATE analyze_truncate")
					if sequence != "truncate_then_empty_scan" {
						enginetest.SessionSQL(t, s, "INSERT INTO analyze_truncate VALUES(4),(4)")
						wantRows, wantTuples = []int64{4, 4}, 2
					}
					if sequence == "savepoint_discards_typed_scan" {
						enginetest.SessionSQL(t, s, "SAVEPOINT before_scan")
					}
					enginetest.SessionSQL(t, s, "ANALYZE analyze_truncate")
				}
				journal := s.Transactions.Catalog
				shape := journal.Storage[ref.Address]
				if !shape.TruncateScanReset || shape.Truncated == rewrite || shape.Original != old || shape.Replacement == nil || shape.Replacement == old {
					t.Fatal("actual TRUNCATE omitted private storage authority", shape)
				}
				delta, exists := journal.Statistics[ref.Address]
				if !exists || delta.Original != old || delta.OriginalRelation == nil {
					t.Fatal("actual ANALYZE omitted composed statistics authority")
				}
				if wantTuples == 2 {
					fact := delta.Columns[key]
					if fact == nil || fact.Rows != 2 || fact.DistinctCount != 1 || len(fact.MCV) != 1 || fact.MCV[0].Count != 2 {
						t.Fatal("post-TRUNCATE collection did not use own inserted population", fact)
					}
				}
				clone := journal.Clone()
				if clone.Storage[ref.Address].Replacement != shape.Replacement || !clone.Storage[ref.Address].TruncateScanReset || clone.Statistics[ref.Address].Columns[key] != delta.Columns[key] {
					t.Fatal("Journal.Clone lost composed truncation/statistics backing")
				}
				wantFact := baseline.Statistics.Columns[key]
				if delta.Columns[key] != nil {
					wantFact = delta.Columns[key]
				}
				if sequence == "savepoint_discards_typed_scan" {
					enginetest.SessionSQL(t, s, "ROLLBACK TO before_scan")
					journal = s.Transactions.Catalog
					reset, retained := journal.Statistics[ref.Address]
					if !retained || len(reset.Columns) != 0 || len(reset.Extended) != 0 || reset.Imported.Mask != 0 || reset.ImportedReset != storage.RelationStatisticsAll || reset.ImportedStorage != shape.Replacement || reset.ImportedAnalyzeCount != baseline.AnalyzeCount || journal.Storage[ref.Address].Replacement != shape.Replacement {
						t.Fatal("savepoint failed to discard collected facts while retaining earlier TRUNCATE reset/storage ownership")
					}
					wantFact = baseline.Statistics.Columns[key]
				}
				if sequence == "savepoint_restores_scanned_truncate" {
					enginetest.SessionSQL(t, s, "SAVEPOINT keep_scan")
					enginetest.SessionSQL(t, s, "TRUNCATE analyze_truncate")
					enginetest.SessionSQL(t, s, "INSERT INTO analyze_truncate VALUES(9)")
					if s.Transactions.Catalog.Storage[ref.Address].Replacement == shape.Replacement {
						t.Fatal("later TRUNCATE did not replace private authority")
					}
					enginetest.SessionSQL(t, s, "ROLLBACK TO keep_scan")
					journal = s.Transactions.Catalog
					if journal.Storage[ref.Address].Replacement != shape.Replacement || journal.Statistics[ref.Address].Columns[key] != delta.Columns[key] {
						t.Fatal("savepoint lost exact scanned-TRUNCATE ownership")
					}
				}
				if shape.Replacement.Maintenance.Scanned != wantScanned || shape.Replacement.Maintenance.Tuples != wantTuples {
					t.Fatal("private successor lost actual ordered scan witness", shape.Replacement.Maintenance)
				}
				collected := *e.Storage.RelationMetadata[ref.Address]
				if collected.AnalyzeCount != baseline.AnalyzeCount+1 || collected.LastAnalyze.IsZero() || collected.LastAnalyze.Before(baseline.LastAnalyze) {
					t.Fatal("successful collection count/time missing before completion")
				}
				if prepared {
					enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'analyze_truncate'")
					entry := core.EnginePreparedRef(e).Lookup("analyze_truncate")
					if entry == nil || entry.Catalog == nil || entry.Catalog.Storage[ref.Address].Replacement != shape.Replacement || !entry.Catalog.Storage[ref.Address].TruncateScanReset || entry.Catalog.Statistics[ref.Address].Columns[key] != journal.Statistics[ref.Address].Columns[key] {
						t.Fatal("PREPARE lost actual composed journal ownership")
					}
					enginetest.SessionSQL(t, finisher, "COMMIT PREPARED 'analyze_truncate'")
					if core.EnginePreparedRef(e).Lookup("analyze_truncate") != nil {
						t.Fatal("prepared TRUNCATE/statistics owner survived completion")
					}
				} else {
					enginetest.SessionSQL(t, s, "COMMIT")
				}
				fresh := e.Storage.Relations[ref.Address]
				if fresh == old || fresh.Maintenance.Scanned != wantScanned || fresh.Maintenance.Tuples != wantTuples {
					t.Fatal("completion reversed TRUNCATE/ANALYZE scan ordering", fresh.Maintenance, wantScanned, wantTuples)
				}
				metadata := *e.Storage.RelationMetadata[ref.Address]
				metadata.Statistics = collected.Statistics
				if metadata != collected {
					t.Fatal("completion changed successful collection count/time or scan metadata")
				}
				if analyzeFacts(e, ref.Address).Columns[key] != wantFact {
					t.Fatal("completion lost selected typed statistics backing")
				}
				catalogTuples, explainRows := wantTuples, wantTuples
				if !wantScanned {
					catalogTuples, explainRows = -1, 1000
				}
				enginetest.TransactionRows(t, finisher, "SELECT reltuples::bigint FROM pg_class WHERE relname='analyze_truncate'", catalogTuples)
				explanation := enginetest.Tuples(enginetest.SessionSQL(t, finisher, "EXPLAIN SELECT a FROM analyze_truncate"))
				if len(explanation) == 0 || len(explanation[0]) != 1 {
					t.Fatal("missing actual EXPLAIN output", explanation)
				}
				line, ok := explanation[0][0].(string)
				if !ok || !strings.Contains(line, "rows="+strconv.FormatInt(explainRows, 10)+" ") {
					t.Fatal("EXPLAIN did not consume ordered physical scan authority", explanation, explainRows)
				}
				enginetest.TransactionRows(t, finisher, "SELECT a FROM analyze_truncate ORDER BY a", wantRows...)
				enginetest.LockEmpty(t, e)
			})
		}
	}
	for _, sequence := range []string{"scan_then_rewrite", "truncate_then_rewrite_without_scan", "savepoint_restores_predecessor_then_rewrite"} {
		for _, prepared := range []bool{false, true} {
			t.Run(sequence+map[bool]string{false: "/commit", true: "/prepared"}[prepared], func(t *testing.T) {
				analyzePostTruncateRewrite(t, sequence, prepared)
			})
		}
	}
}

func TestAnalyzeInheritedDeltaTombstones(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE analyze_delta_parent(a INT,b INT)",
		"CREATE TABLE analyze_delta_child() INHERITS(analyze_delta_parent)",
		"INSERT INTO analyze_delta_parent VALUES(1,10),(1,10)",
		"INSERT INTO analyze_delta_child VALUES(2,20),(2,20),(2,20)",
		"ANALYZE analyze_delta_parent",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, _ := vacuumStore(t, e, "analyze_delta_parent")
	relation := e.CatalogRead().MustRelation(ref.Address)
	baseline := analyzeFacts(e, ref.Address)
	inheritedA := storage.StatisticsKey{Attribute: relation.Attributes[0], Inherited: true}
	inheritedB := storage.StatisticsKey{Attribute: relation.Attributes[1], Inherited: true}
	if baseline.Columns[inheritedA] == nil || baseline.Columns[inheritedB] == nil {
		t.Fatal("fixture omitted both inherited attribute facts")
	}
	for _, q := range []string{"BEGIN", "ALTER TABLE analyze_delta_child NO INHERIT analyze_delta_parent", "ANALYZE analyze_delta_parent(a)"} {
		enginetest.SessionSQL(t, s, q)
	}
	journal := s.Transactions.Catalog
	delta := journal.Statistics[ref.Address]
	for _, key := range []storage.StatisticsKey{inheritedA, inheritedB} {
		fact, present := delta.Columns[key]
		if !present || fact != nil {
			t.Fatal("selected-column refresh lost required inherited deletion tombstone", key)
		}
		if _, present := journal.Clone().Statistics[ref.Address].Columns[key]; !present {
			t.Fatal("Journal.Clone discarded inherited deletion authority", key)
		}
	}
	e.Mu.Lock()
	view := e.PrivateCatalogLocked(journal)
	privateRelation, exists := view.Relation(ref.Address)
	own := session.ResolveStatistics(journal, ref.Address, e.Storage.RelationMetadata[ref.Address], privateRelation, view)
	e.Mu.Unlock()
	if !exists || own == nil || own.Columns[inheritedA] != nil || own.Columns[inheritedB] != nil || analyzeFacts(e, ref.Address) != baseline {
		t.Fatal("own resolver ignored tombstones or leaked pending typed deletions")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
	if analyzeFacts(e, ref.Address) != baseline || baseline.Columns[inheritedA] == nil || baseline.Columns[inheritedB] == nil {
		t.Fatal("rollback lost committed inherited backing")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE analyze_delta_child NO INHERIT analyze_delta_parent")
	enginetest.SessionSQL(t, s, "ANALYZE analyze_delta_parent(a)")
	metadata := e.Storage.RelationMetadata[ref.Address]
	if metadata.InheritedScanned || metadata.InheritedTuples != 0 || metadata.Statistics.Columns[inheritedA] != nil || metadata.Statistics.Columns[inheritedB] != nil {
		t.Fatal("committed local-only refresh retained obsolete inherited authority")
	}
	enginetest.LockEmpty(t, e)
}

func analyzePostTruncateRewrite(t *testing.T, sequence string, prepared bool) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	finisher := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE analyze_successor(a INT,b INT)",
		"INSERT INTO analyze_successor VALUES(1,10),(1,20),(2,30)",
		"ANALYZE analyze_successor",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, committed := vacuumStore(t, e, "analyze_successor")
	baseline := e.Storage.RelationMetadata[ref.Address]
	relation := e.CatalogRead().MustRelation(ref.Address)
	key := storage.StatisticsKey{Attribute: relation.Attributes[0]}
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "TRUNCATE analyze_successor")
	scanned := sequence != "truncate_then_rewrite_without_scan"
	wantTuples, wantCount := int64(0), baseline.AnalyzeCount
	wantFact := baseline.Statistics.Columns[key]
	if scanned {
		enginetest.SessionSQL(t, s, "INSERT INTO analyze_successor VALUES(4,40),(4,40)")
		enginetest.SessionSQL(t, s, "ANALYZE analyze_successor(a)")
		wantTuples, wantCount = 2, baseline.AnalyzeCount+1
		wantFact = s.Transactions.Catalog.Statistics[ref.Address].Columns[key]
		if wantFact == nil || wantFact.Rows != 2 || wantFact.DistinctCount != 1 || len(wantFact.MCV) != 1 || wantFact.MCV[0].Count != 2 {
			t.Fatal("actual post-TRUNCATE collection omitted two inserted rows", wantFact)
		}
	}
	collected := *e.Storage.RelationMetadata[ref.Address]
	predecessorJournal := s.Transactions.Catalog
	predecessor := predecessorJournal.Storage[ref.Address]
	if predecessor.Original != committed || predecessor.Replacement == nil || !predecessor.Truncated || !predecessor.TruncateScanReset || predecessor.Replacement.Maintenance.Scanned != scanned || predecessor.Replacement.Maintenance.Tuples != wantTuples {
		t.Fatal("actual TRUNCATE predecessor lacks ordered scan provenance", predecessor)
	}
	predecessorRows, predecessorMaintenance := predecessor.Replacement.Versions, predecessor.Replacement.Maintenance
	rewrite := func() session.StorageChange {
		t.Helper()
		// Changing b forces an actual physical rewrite while a retains exactly
		// the same stable attribute and concrete type as the captured statistic.
		enginetest.SessionSQL(t, s, "ALTER TABLE analyze_successor ALTER COLUMN b TYPE BIGINT")
		successor := s.Transactions.Catalog.Storage[ref.Address]
		if successor.Original != committed || successor.Replacement == nil || successor.Replacement == predecessor.Replacement || successor.Truncated || !successor.TruncateScanReset {
			t.Fatal("compatible ALTER did not create a distinct composed successor", successor)
		}
		if successor.Replacement.Maintenance.Scanned != scanned || successor.Replacement.Maintenance.Tuples != wantTuples {
			t.Fatal("later rewrite lost real scan or manufactured pre-TRUNCATE scan", successor.Replacement.Maintenance)
		}
		if !storage.SameVersionRoot(predecessorRows, predecessor.Replacement.Versions) || predecessor.Replacement.Maintenance != predecessorMaintenance || predecessorJournal.Storage[ref.Address].Replacement != predecessor.Replacement {
			t.Fatal("later rewrite mutated retained predecessor storage or Journal authority")
		}
		return successor
	}
	if sequence == "savepoint_restores_predecessor_then_rewrite" {
		enginetest.SessionSQL(t, s, "SAVEPOINT before_rewrite")
		discarded := rewrite()
		enginetest.SessionSQL(t, s, "ROLLBACK TO before_rewrite")
		restored := s.Transactions.Catalog.Storage[ref.Address]
		if restored.Replacement != predecessor.Replacement || restored.Replacement == discarded.Replacement || !restored.Truncated || !restored.TruncateScanReset || restored.Replacement.Maintenance.Scanned != scanned || restored.Replacement.Maintenance.Tuples != wantTuples {
			t.Fatal("savepoint did not restore exact predecessor and scan authority", restored)
		}
	}
	successor := rewrite()
	journal := s.Transactions.Catalog
	if clone := journal.Clone(); clone.Storage[ref.Address].Replacement != successor.Replacement || !clone.Storage[ref.Address].TruncateScanReset || clone.Storage[ref.Address].Truncated || scanned && clone.Statistics[ref.Address].Columns[key] != wantFact {
		t.Fatal("Journal.Clone lost later-successor identity, truncate provenance or selected backing")
	}
	if prepared {
		enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'analyze_successor'")
		entry := core.EnginePreparedRef(e).Lookup("analyze_successor")
		if entry == nil || entry.Catalog == nil || entry.Catalog.Storage[ref.Address].Replacement != successor.Replacement || !entry.Catalog.Storage[ref.Address].TruncateScanReset || entry.Catalog.Storage[ref.Address].Truncated || scanned && entry.Catalog.Statistics[ref.Address].Columns[key] != wantFact {
			t.Fatal("PREPARE lost later rewrite successor or selected backing ownership")
		}
		enginetest.SessionSQL(t, finisher, "COMMIT PREPARED 'analyze_successor'")
		if core.EnginePreparedRef(e).Lookup("analyze_successor") != nil {
			t.Fatal("completed prepared successor still registered")
		}
	} else {
		enginetest.SessionSQL(t, s, "COMMIT")
	}
	fresh := e.Storage.Relations[ref.Address]
	if fresh == committed || fresh != successor.Replacement || fresh.Maintenance.Scanned != scanned || fresh.Maintenance.Tuples != wantTuples {
		t.Fatal("completion lost later successor scan authority", fresh.Maintenance)
	}
	metadata := *e.Storage.RelationMetadata[ref.Address]
	if metadata.Statistics.Columns[key] != wantFact || metadata.AnalyzeCount != wantCount || metadata.LastAnalyze != collected.LastAnalyze {
		t.Fatal("rewrite completion changed selected backing or actual collection count/time")
	}
	metadata.Statistics = collected.Statistics
	if metadata != collected {
		t.Fatal("rewrite completion changed canonical scan metadata")
	}
	wantCatalog, wantExplain := wantTuples, wantTuples
	if !scanned {
		wantCatalog, wantExplain = -1, 1000
	}
	enginetest.TransactionRows(t, finisher, "SELECT reltuples::bigint FROM pg_class WHERE relname='analyze_successor'", wantCatalog)
	explanation := enginetest.Tuples(enginetest.SessionSQL(t, finisher, "EXPLAIN SELECT a FROM analyze_successor"))
	if len(explanation) == 0 || len(explanation[0]) != 1 {
		t.Fatal("missing successor EXPLAIN output", explanation)
	}
	line, ok := explanation[0][0].(string)
	if !ok || !strings.Contains(line, "rows="+strconv.FormatInt(wantExplain, 10)+" ") {
		t.Fatal("EXPLAIN ignored genuine later successor scan authority", explanation, wantExplain)
	}
	if scanned {
		enginetest.TransactionRows(t, finisher, "SELECT a FROM analyze_successor ORDER BY a", 4, 4)
	} else {
		enginetest.TransactionRows(t, finisher, "SELECT a FROM analyze_successor ORDER BY a")
	}
	enginetest.LockEmpty(t, e)
}
