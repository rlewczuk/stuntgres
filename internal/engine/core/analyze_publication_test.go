package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestAnalyzeJournalOwnership(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'analyze_owner'", "ROLLBACK PREPARED 'analyze_owner'"} {
		t.Run(completion, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE analyze_owner(a INT,b INT)", "INSERT INTO analyze_owner VALUES(1,10),(1,10),(2,20)", "ANALYZE analyze_owner"} {
				enginetest.SessionSQL(t, s, q)
			}
			ref, store := vacuumStore(t, e, "analyze_owner")
			relation := e.CatalogRead().MustRelation(ref.Address)
			a, b := storage.StatisticsKey{Attribute: relation.Attributes[0]}, storage.StatisticsKey{Attribute: relation.Attributes[1]}
			baseline := analyzeFacts(e, ref.Address)
			if baseline == nil || baseline.Columns[a] == nil || baseline.Columns[b] == nil {
				t.Fatal("actual ANALYZE omitted canonical baseline")
			}
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, "ANALYZE analyze_owner(a)")
			first := s.Transactions.Catalog
			selected := first.Statistics[ref.Address]
			if selected.Original != store || selected.OriginalRelation != relation || selected.Relation != relation || len(selected.Columns) != 1 || selected.Columns[a] == nil {
				t.Fatal("statement Finish did not transfer actual selected delta", selected)
			}
			clone := first.Clone()
			if clone == first || clone.Statistics[ref.Address].Columns[a] != selected.Columns[a] {
				t.Fatal("Journal.Clone lost immutable selected backing")
			}
			enginetest.SessionSQL(t, s, "SAVEPOINT kept")
			enginetest.SessionSQL(t, s, "ANALYZE analyze_owner(b)")
			latest := s.Transactions.Catalog.Statistics[ref.Address]
			if len(latest.Columns) != 2 || latest.Columns[a] != selected.Columns[a] || latest.Columns[b] == baseline.Columns[b] {
				t.Fatal("later statement did not merge actual disjoint selected facts")
			}
			if len(first.Statistics[ref.Address].Columns) != 1 || len(clone.Statistics[ref.Address].Columns) != 1 {
				t.Fatal("statement merge mutated retained journal/map")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK TO kept")
			kept := s.Transactions.Catalog
			if len(kept.Statistics[ref.Address].Columns) != 1 || kept.Statistics[ref.Address].Columns[a] != selected.Columns[a] {
				t.Fatal("savepoint failed to restore exact column backing")
			}
			own := session.ResolveStatistics(kept, ref.Address, e.Storage.RelationMetadata[ref.Address], relation, e.CatalogRead())
			if own.Columns[a] != selected.Columns[a] || own.Columns[b] != baseline.Columns[b] || analyzeFacts(e, ref.Address) != baseline {
				t.Fatal("private resolver leaked or recopied unselected canonical facts")
			}
			// An unrelated column is refreshed after this transaction's capture.
			// Commit must merge its selected delta into that newer canonical root.
			enginetest.SessionSQL(t, peer, "ANALYZE analyze_owner(b)")
			newB := analyzeFacts(e, ref.Address).Columns[b]
			if newB == baseline.Columns[b] || analyzeFacts(e, ref.Address).Columns[a] != baseline.Columns[a] {
				t.Fatal("peer selected refresh did not preserve the other column")
			}
			rows, ends, generation := store.Versions, store.Ends, store.Generation
			rowFrontier, versionFrontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			maintenance := store.Maintenance
			analyzeMaintenance := *e.Storage.RelationMetadata[ref.Address]
			if strings.Contains(completion, "PREPARED") {
				enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'analyze_owner'")
				entry := core.EnginePreparedRef(e).Lookup("analyze_owner")
				if entry == nil || entry.Catalog == nil || entry.Catalog.Statistics[ref.Address].Columns[a] != selected.Columns[a] || s.Transactions.Catalog != nil {
					t.Fatal("PREPARE failed to detach real statistics journal ownership")
				}
				enginetest.SessionSQL(t, peer, completion)
				if core.EnginePreparedRef(e).Lookup("analyze_owner") != nil {
					t.Fatal("prepared statistics owner retained after completion")
				}
			} else {
				enginetest.SessionSQL(t, s, completion)
			}
			wantA := baseline.Columns[a]
			if strings.HasPrefix(completion, "COMMIT") {
				wantA = selected.Columns[a]
			}
			if analyzeFacts(e, ref.Address).Columns[a] != wantA || analyzeFacts(e, ref.Address).Columns[b] != newB {
				t.Fatal("completion lost selected owner or unrelated newer backing")
			}
			if e.Storage.Relations[ref.Address] != store || !storage.SameVersionRoot(rows, store.Versions) || store.Ends != ends || store.Generation != generation || e.Storage.RowFrontier() != rowFrontier || e.Storage.VersionFrontier() != versionFrontier || store.Maintenance != maintenance {
				t.Fatal("statistics-only completion rewrote rows, allocated tuple identities, or changed maintenance counters")
			}
			completedMetadata := *e.Storage.RelationMetadata[ref.Address]
			completedMetadata.Statistics = analyzeMaintenance.Statistics
			if completedMetadata != analyzeMaintenance {
				t.Fatal("statistics-only completion changed ANALYZE scan facts or counters")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestAnalyzePrivateSchemaComposition(t *testing.T) {
	for _, ddlFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "statistics_before_projection", true: "projection_before_statistics"}[ddlFirst], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE analyze_schema(a INT,b INT)", "INSERT INTO analyze_schema VALUES(1,10),(2,10)", "ANALYZE analyze_schema", "BEGIN"} {
				enginetest.SessionSQL(t, s, q)
			}
			ref, old := vacuumStore(t, e, "analyze_schema")
			relation := e.CatalogRead().MustRelation(ref.Address)
			key := storage.StatisticsKey{Attribute: relation.Attributes[1]}
			beforeMetadata := e.Storage.RelationMetadata[ref.Address]
			before := beforeMetadata.Statistics
			if ddlFirst {
				enginetest.SessionSQL(t, s, "ALTER TABLE analyze_schema DROP COLUMN a")
			}
			enginetest.SessionSQL(t, s, "ANALYZE analyze_schema(b)")
			selected := s.Transactions.Catalog.Statistics[ref.Address].Columns[key]
			if selected == nil || selected.Rows != 2 || selected.DistinctCount != 1 {
				t.Fatal("private schema collection did not use mapped actual stored values")
			}
			if !ddlFirst {
				enginetest.SessionSQL(t, s, "ALTER TABLE analyze_schema DROP COLUMN a")
			}
			enginetest.SessionSQL(t, s, "INSERT INTO analyze_schema(b) VALUES(20)")
			enginetest.SessionSQL(t, s, "COMMIT")
			fresh := e.Storage.Relations[ref.Address]
			facts := analyzeFacts(e, ref.Address)
			if fresh == old || len(fresh.Versions) != 3 || facts == nil || facts.Columns[key] != selected || len(facts.Columns) != 1 {
				t.Fatal("DDL projection/DML publication lost stable-attribute statistics ownership")
			}
			if beforeMetadata.Statistics != before || len(before.Columns) != 2 {
				t.Fatal("private projection mutated retained old statistics")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestAnalyzeNativeStorageRemint(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE analyze_native(a INT)", "INSERT INTO analyze_native VALUES(1),(1),(2)", "ANALYZE analyze_native"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, old := vacuumStore(t, e, "analyze_native")
	metadata := e.Storage.RelationMetadata[ref.Address]
	facts, rows := metadata.Statistics, old.Versions
	var dump strings.Builder
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE broken(")); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	_, retained := vacuumStore(t, e, "analyze_native")
	if retained != old || e.Storage.RelationMetadata[ref.Address] != metadata {
		t.Fatal("failed restore lost actual canonical publication")
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	restoredRef, restored := vacuumStore(t, e, "analyze_native")
	restoredFacts := analyzeFacts(e, restoredRef.Address)
	if restored == old || restoredFacts == nil || restoredFacts == facts || len(restoredFacts.Columns) != 1 || len(restored.Versions) != 3 {
		t.Fatal("native restore failed to remint column statistics or lost data")
	}
	key := storage.StatisticsKey{Attribute: 1}
	fact := restoredFacts.Columns[key]
	current := e.CatalogRead().MustRelation(restoredRef.Address)
	if fact == nil || fact == facts.Columns[key] || fact.Rows != 3 || fact.NullCount != 0 || !fact.DistinctKnown || fact.DistinctCount != 2 || len(fact.MCV) != len(facts.Columns[key].MCV) || len(fact.Histogram) != len(facts.Columns[key].Histogram) || fact.Usage != current.Columns[0].Usage {
		t.Fatal("native restore lost measured scalar distributions or typed ownership")
	}
	for i, item := range fact.MCV {
		if item.Count != facts.Columns[key].MCV[i].Count || !item.Value.ValidResult(fact.Usage) {
			t.Fatal("native restore changed measured MCV counts or retained foreign value authority")
		}
	}
	for _, value := range fact.Histogram {
		if !value.ValidResult(fact.Usage) {
			t.Fatal("native restore retained foreign histogram authority")
		}
	}
	enginetest.TransactionRows(t, s, "SELECT a FROM analyze_native ORDER BY a", 1, 1, 2)
	if metadata.Statistics != facts || !storage.SameVersionRoot(rows, old.Versions) {
		t.Fatal("native storage remint mutated retained source")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE analyze_native(a INT)", "INSERT INTO analyze_native VALUES(9)"} {
		enginetest.SessionSQL(t, s, q)
	}
	resetRef, reset := vacuumStore(t, e, "analyze_native")
	if reset == restored || analyzeFacts(e, resetRef.Address) != nil {
		t.Fatal("reset/recreate attached obsolete typed facts")
	}
}

func TestAnalyzePreparedAuthorityRejection(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE analyze_rejected(a INT)", "INSERT INTO analyze_rejected VALUES(1),(1),(2)", "ANALYZE analyze_rejected", "BEGIN", "ANALYZE analyze_rejected", "PREPARE TRANSACTION 'analyze_rejected'"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, old := vacuumStore(t, e, "analyze_rejected")
	entry := core.EnginePreparedRef(e).Lookup("analyze_rejected")
	if entry == nil || entry.Catalog == nil || len(entry.Catalog.Statistics) != 1 {
		t.Fatal("actual prepared statistics-only owner was discarded")
	}
	metadata, maintenance := e.Storage.RelationMetadata[ref.Address], old.Maintenance
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	replacement := *old
	stage.RowShapes = map[ids.ObjectAddress]storage.RowShape{ref.Address: {
		Original: old, Root: old.Versions, Replacement: &replacement,
		ObservedLength: len(old.Versions), ObservedEnds: old.Ends, Generation: old.Generation,
	}}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	op, err := peer.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "COMMIT PREPARED 'analyze_rejected'"), 0, nil)
	op.Done()
	if err == nil {
		t.Fatal("prepared statistics installed into a reminted store")
	}
	if core.EnginePreparedRef(e).Lookup("analyze_rejected") != entry || e.Storage.RelationMetadata[ref.Address] != metadata || old.Maintenance != maintenance {
		t.Fatal("rejected prepared completion consumed ownership or changed facts")
	}
	enginetest.SessionSQL(t, peer, "ROLLBACK PREPARED 'analyze_rejected'")
	enginetest.LockEmpty(t, e)
}

func TestAnalyzeStorageLifecycle(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE analyze_lifecycle(a INT,b INT)", "INSERT INTO analyze_lifecycle VALUES(1,10),(1,10),(2,20)", "ANALYZE analyze_lifecycle"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, original := vacuumStore(t, e, "analyze_lifecycle")
	relation := e.CatalogRead().MustRelation(ref.Address)
	key := storage.StatisticsKey{Attribute: relation.Attributes[1]}
	selected := analyzeFacts(e, ref.Address).Columns[key]
	for _, q := range []string{"ALTER TABLE analyze_lifecycle RENAME TO analyze_renamed", "INSERT INTO analyze_renamed VALUES(3,30)", "UPDATE analyze_renamed SET b=40 WHERE a=2", "DELETE FROM analyze_renamed WHERE a=3", "VACUUM analyze_renamed"} {
		enginetest.SessionSQL(t, s, q)
		if analyzeFacts(e, ref.Address).Columns[key] != selected {
			t.Fatal("compatible successor failed to retain stale typed backing", q)
		}
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE analyze_renamed DROP COLUMN a")
	projected := analyzeFacts(e, ref.Address)
	if projected == nil || projected.Columns[key] != selected || len(projected.Columns) != 1 {
		t.Fatal("dropping first physical slot shifted or retained incompatible attribute facts")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE analyze_renamed ALTER COLUMN b TYPE BIGINT")
	if facts := analyzeFacts(e, ref.Address); facts != nil && facts.Columns[key] != nil {
		t.Fatal("incompatible type rewrite retained old concrete typed statistics")
	}
	enginetest.SessionSQL(t, s, "DROP TABLE analyze_renamed")
	enginetest.SessionSQL(t, s, "CREATE TABLE analyze_renamed(b INT)")
	freshRef, fresh := vacuumStore(t, e, "analyze_renamed")
	if freshRef == ref || fresh == original || analyzeFacts(e, freshRef.Address) != nil {
		t.Fatal("drop/recreate attached facts by display name")
	}
}

func TestAnalyzeTransactionVisiblePopulation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE analyze_visible(a INT)",
		"INSERT INTO analyze_visible VALUES(1),(1),(1),(1),(1),(1),(1),(1),(1),(1)",
		"CREATE TABLE analyze_second(a INT)",
		"INSERT INTO analyze_second VALUES(8),(8)",
		"ANALYZE analyze_visible, analyze_second",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	firstRef, _ := vacuumStore(t, e, "analyze_visible")
	secondRef, _ := vacuumStore(t, e, "analyze_second")
	firstBaseline, secondBaseline := analyzeFacts(e, firstRef.Address), analyzeFacts(e, secondRef.Address)
	firstRelation := e.CatalogRead().MustRelation(firstRef.Address)
	secondRelation := e.CatalogRead().MustRelation(secondRef.Address)
	firstKey := storage.StatisticsKey{Attribute: firstRelation.Attributes[0]}
	secondKey := storage.StatisticsKey{Attribute: secondRelation.Attributes[0]}
	for _, q := range []string{
		"BEGIN",
		"INSERT INTO analyze_visible VALUES(2),(2),(2),(2),(2),(2),(2),(2),(2),(2)",
		"UPDATE analyze_visible SET a=3",
		"ANALYZE analyze_visible, analyze_second",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	first := s.Transactions.Catalog.Statistics[firstRef.Address].Columns[firstKey]
	second := s.Transactions.Catalog.Statistics[secondRef.Address].Columns[secondKey]
	if first == nil || first.Rows != 20 || first.NullCount != 0 || first.DistinctCount != 1 || len(first.MCV) != 1 || first.MCV[0].Count != 20 {
		t.Fatal("collector counted committed UPDATE predecessors beside own replacements", first)
	}
	if second == nil || second.Rows != 2 || second.DistinctCount != 1 || len(second.MCV) != 1 || second.MCV[0].Count != 2 {
		t.Fatal("later target reused released or mismatched first-target data capture", second)
	}
	if analyzeFacts(e, firstRef.Address) != firstBaseline || analyzeFacts(e, secondRef.Address) != secondBaseline {
		t.Fatal("multiple-target private statistics leaked canonical publication")
	}
	// Repeating the command traverses fresh target captures again, while exact
	// retained facts remain private and previous immutable payloads stay owned.
	enginetest.SessionSQL(t, s, "ANALYZE analyze_second, analyze_visible")
	latest := s.Transactions.Catalog.Statistics[firstRef.Address].Columns[firstKey]
	if latest == first || latest.Rows != 20 || latest.DistinctCount != 1 || first.Rows != 20 || first.MCV[0].Count != 20 {
		t.Fatal("repeated target capture reused scratch or mutated retained facts")
	}
	enginetest.SessionSQL(t, s, "COMMIT")
	if analyzeFacts(e, firstRef.Address).Columns[firstKey] != latest || analyzeFacts(e, secondRef.Address).Columns[secondKey].Rows != 2 {
		t.Fatal("completion lost actual multi-target private publications")
	}
	enginetest.LockEmpty(t, e)
}

func TestAnalyzePartitionMetadataOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	// Direct standalone CREATE must not turn the partitioned descriptor into
	// a physical heap merely to retain its later ANALYZE publication.
	enginetest.SessionSQL(t, s, "CREATE TABLE analyze_parent_owner(a INT) PARTITION BY RANGE(a)")
	parentRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "analyze_parent_owner")
	if e.Storage.Relations[parentRef.Address] != nil || analyzeFacts(e, parentRef.Address) != nil {
		t.Fatal("partition parent creation fabricated a heap or planner facts")
	}
	enginetest.SessionSQL(t, s, "CREATE TABLE analyze_leaf_owner PARTITION OF analyze_parent_owner FOR VALUES FROM(0) TO(10)")
	leafRef, leaf := vacuumStore(t, e, "analyze_leaf_owner")
	if leafRef == parentRef || leaf.Maintenance.FrozenXID == 0 {
		t.Fatal("partition leaf did not receive independent physical storage")
	}
	enginetest.SessionSQL(t, s, "INSERT INTO analyze_parent_owner VALUES(2),(2),(2),(2)")
	if e.Storage.Relations[parentRef.Address] != nil || len(leaf.Versions) != 4 {
		t.Fatal("partition routing stored rows on metadata-only parent")
	}
	leafMaintenance := leaf.Maintenance
	enginetest.SessionSQL(t, s, "ANALYZE ONLY analyze_parent_owner")
	relation := e.CatalogRead().MustRelation(parentRef.Address)
	key := storage.StatisticsKey{Attribute: relation.Attributes[0], Inherited: true}
	parent := e.Storage.RelationMetadata[parentRef.Address]
	if parent == nil || parent.Statistics == nil {
		t.Fatal("parent canonical metadata did not receive inherited statistics")
	}
	fact := parent.Statistics.Columns[key]
	if fact == nil || fact.Rows != 4 || fact.DistinctCount != 1 || len(fact.MCV) != 1 || fact.MCV[0].Count != 4 {
		t.Fatal("ONLY parent collection omitted actual descendant population", fact)
	}
	if e.Storage.Relations[parentRef.Address] != nil || parent.AnalyzeCount != 1 || !parent.InheritedScanned || parent.InheritedTuples != 4 {
		t.Fatal("parent collection fabricated physical storage or omitted successful collection")
	}
	if analyzeFacts(e, leafRef.Address) != nil || leaf.Maintenance != leafMaintenance || leaf.Maintenance.Scanned {
		t.Fatal("ONLY parent collection independently analyzed descendant storage")
	}
	enginetest.LockEmpty(t, e)
}

func analyzeFacts(e *core.Engine, address ids.ObjectAddress) *storage.RelationStatistics {
	if metadata := e.Storage.RelationMetadata[address]; metadata != nil {
		return metadata.Statistics
	}
	return nil
}

func TestAnalyzePrivateMetadataOwnership(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(completion, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"BEGIN",
				"CREATE TABLE analyze_private_parent(a INT) PARTITION BY RANGE(a)",
				"CREATE TABLE analyze_private_leaf PARTITION OF analyze_private_parent FOR VALUES FROM(0) TO(10)",
				"INSERT INTO analyze_private_parent VALUES(2),(2)",
				"ANALYZE ONLY analyze_private_parent",
			} {
				enginetest.SessionSQL(t, s, q)
			}
			var address ids.ObjectAddress
			var change session.StatisticsChange
			for candidate, delta := range s.Transactions.Catalog.Statistics {
				if delta.Relation.Name() == "analyze_private_parent" {
					address, change = candidate, delta
				}
			}
			if change.PrivateMetadata == nil || change.Original != nil || change.OriginalRelation != nil || change.PrivateMetadata.AnalyzeCount != 1 || change.PrivateMetadata.InheritedTuples != 2 {
				t.Fatal("new private partition did not retain real private metadata authority", change)
			}
			if e.Storage.Relations[address] != nil || e.Storage.RelationMetadata[address] != nil {
				t.Fatal("private partition leaked a physical heap or committed metadata")
			}
			clone := s.Transactions.Catalog.Clone()
			if clone.Statistics[address].PrivateMetadata != change.PrivateMetadata {
				t.Fatal("Journal.Clone lost private metadata backing")
			}
			enginetest.SessionSQL(t, s, completion)
			if e.Storage.Relations[address] != nil {
				t.Fatal("private partition completion fabricated a physical heap")
			}
			if completion == "ROLLBACK" {
				if e.Storage.RelationMetadata[address] != nil {
					t.Fatal("rollback leaked private relation metadata")
				}
			} else {
				metadata := e.Storage.RelationMetadata[address]
				if metadata == nil || metadata.AnalyzeCount != 1 || metadata.InheritedTuples != 2 || metadata.Statistics == nil {
					t.Fatal("commit lost private partition metadata")
				}
				for key, fact := range change.Columns {
					if metadata.Statistics.Columns[key] != fact {
						t.Fatal("private metadata commit lost selected statistics backing")
					}
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
