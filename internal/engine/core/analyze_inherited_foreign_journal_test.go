package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestAnalyzeInheritedForeignJournalOwnership(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'mixed_journal'", "ROLLBACK PREPARED 'mixed_journal'"} {
			t.Run(map[bool]string{false: "ordinary/", true: "partition/"}[partition]+completion, func(t *testing.T) {
				f := newInheritedForeignFixture(t, 10, partition)
				f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_journal_expression ON (a+b) FROM mixed_parent")
				f.analyze(t, false, "")
				baseline := f.e.Storage.RelationMetadata[f.parent.Address].Statistics
				retained := f.retained(t)
				relation := f.e.CatalogRead().MustRelation(f.parent.Address)
				a, b := storage.StatisticsKey{Attribute: relation.Attributes[0], Inherited: true}, storage.StatisticsKey{Attribute: relation.Attributes[1], Inherited: true}
				extendedKey := ndistinctKey(t, f.e, "mixed_journal_expression")
				extendedKey.Inherited = true
				peer := enginetest.StartSession(t, f.e, nil)
				enginetest.SessionSQL(t, f.s, "BEGIN")
				f.analyze(t, true, "(a)")
				first := f.s.Transactions.Catalog
				selected := first.Statistics[f.parent.Address]
				wantColumns := 2
				if partition {
					wantColumns = 1
				}
				if len(selected.Columns) != wantColumns || selected.Columns[a] == nil || selected.Columns[a] == baseline.Columns[a] || enginetest.AnalyzeFacts(f.e, f.parent.Address) != baseline {
					t.Fatal("mixed typed facts not actual Journal-private deltas", selected)
				}
				selectedExtended := selected.Extended[extendedKey]
				if selectedExtended == nil || selectedExtended == baseline.Extended[extendedKey] {
					t.Fatal("mixed expression was not retained on actual selected Journal")
				}
				keepExtended := retainMixedExtended(t, selectedExtended)
				clone := first.Clone()
				if clone.Statistics[f.parent.Address].Columns[a] != selected.Columns[a] {
					t.Fatal("clone copied immutable mixed payload")
				}
				if clone.Statistics[f.parent.Address].Extended[extendedKey] != selectedExtended {
					t.Fatal("clone copied immutable mixed expression backing")
				}
				enginetest.SessionSQL(t, f.s, "SAVEPOINT kept")
				f.analyze(t, false, "(b)")
				if len(first.Statistics[f.parent.Address].Columns) != wantColumns {
					t.Fatal("selected merge mutated predecessor")
				}
				enginetest.SessionSQL(t, f.s, "ROLLBACK TO kept")
				own := session.ResolveStatistics(f.s.Transactions.Catalog, f.parent.Address, f.e.Storage.RelationMetadata[f.parent.Address], relation, f.e.CatalogRead())
				if own.Columns[a] != selected.Columns[a] || own.Columns[b] != baseline.Columns[b] {
					t.Fatal("savepoint lost exact selected/unselected backing")
				}
				if own.Extended[extendedKey] != selectedExtended {
					t.Fatal("savepoint lost actual extended Journal backing")
				}
				enginetest.SessionSQL(t, peer, "ANALYZE ONLY mixed_parent(b)")
				newB := enginetest.AnalyzeFacts(f.e, f.parent.Address).Columns[b]
				newExtended := enginetest.AnalyzeFacts(f.e, f.parent.Address).Extended[extendedKey]
				maintenance := *f.e.Storage.RelationMetadata[f.parent.Address]
				rows, versions := f.e.Storage.RowFrontier(), f.e.Storage.VersionFrontier()
				if strings.Contains(completion, "PREPARED") {
					enginetest.SessionSQL(t, f.s, "PREPARE TRANSACTION 'mixed_journal'")
					entry := core.EnginePreparedRef(f.e).Lookup("mixed_journal")
					if entry == nil || entry.Catalog.Statistics[f.parent.Address].Columns[a] != selected.Columns[a] || f.s.Transactions.Catalog != nil {
						t.Fatal("prepared owner detached wrong mixed backing")
					}
					if entry.Catalog.Statistics[f.parent.Address].Extended[extendedKey] != selectedExtended {
						t.Fatal("prepared owner detached wrong extended backing")
					}
					enginetest.SessionSQL(t, peer, completion)
				} else {
					enginetest.SessionSQL(t, f.s, completion)
				}
				wantA := baseline.Columns[a]
				if strings.HasPrefix(completion, "COMMIT") {
					wantA = selected.Columns[a]
				}
				after := *f.e.Storage.RelationMetadata[f.parent.Address]
				if after.Statistics.Columns[a] != wantA || after.Statistics.Columns[b] != newB {
					t.Fatal("completion lost selected or newer peer backing")
				}
				wantExtended := newExtended
				if strings.HasPrefix(completion, "COMMIT") {
					wantExtended = selectedExtended
				}
				if after.Statistics.Extended[extendedKey] != wantExtended {
					t.Fatal("completion lost prepared/private or peer extended backing")
				}
				keepExtended()
				after.Statistics = maintenance.Statistics
				if after != maintenance || f.e.Storage.RowFrontier() != rows || f.e.Storage.VersionFrontier() != versions {
					t.Fatal("typed completion replayed maintenance or allocated rows")
				}
				retained()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeInheritedForeignPrivateOwnership(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'mixed_private'", "ROLLBACK PREPARED 'mixed_private'", "savepoint"} {
			t.Run(map[bool]string{false: "ordinary/", true: "partition/"}[partition]+completion, func(t *testing.T) {
				f := newInheritedForeignFixture(t, 0, false)
				f.wrapper.sources["private_foreign"] = &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(2), int64(20)}, {int64(2), int64(20)}}, TotalRows: 30.5, TotalPages: 1}}
				enginetest.SessionSQL(t, f.s, "BEGIN")
				if completion == "savepoint" {
					enginetest.SessionSQL(t, f.s, "SAVEPOINT before_creation")
				}
				if partition {
					enginetest.SessionSQL(t, f.s, "CREATE TABLE private_parent(a integer,b integer) PARTITION BY RANGE(a)")
					enginetest.SessionSQL(t, f.s, "CREATE FOREIGN TABLE private_foreign(a integer,b integer) SERVER mixed_owner_server")
					enginetest.SessionSQL(t, f.s, "ALTER TABLE private_parent ATTACH PARTITION private_foreign FOR VALUES FROM(0) TO(4)")
				} else {
					enginetest.SessionSQL(t, f.s, "CREATE TABLE private_parent(a integer,b integer)")
					enginetest.SessionSQL(t, f.s, "INSERT INTO private_parent VALUES(1,10)")
					enginetest.SessionSQL(t, f.s, "CREATE FOREIGN TABLE private_foreign(a integer,b integer) INHERITS(private_parent) SERVER mixed_owner_server")
				}
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS private_joint(ndistinct,dependencies,mcv) ON a,b FROM private_parent")
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS private_expression ON (a+b) FROM private_parent")
				enginetest.SessionSQL(t, f.s, "ANALYZE ONLY private_parent")
				var address ids.ObjectAddress
				var delta session.StatisticsChange
				for a, change := range f.s.Transactions.Catalog.Statistics {
					if change.Relation.Name() == "private_parent" {
						address, delta = a, change
					}
				}
				wantPopulation := 31.5
				if partition {
					wantPopulation = 30.5
				}
				if delta.PrivateMetadata == nil || delta.PrivateMetadata.AnalyzeCount != 1 || delta.PrivateMetadata.InheritedTuples != wantPopulation || delta.OriginalRelation != nil || f.e.Storage.RelationMetadata[address] != nil || f.e.Storage.Relations[address] != nil {
					t.Fatal("private mixed object leaked canonical ownership", delta)
				}
				wantExtended := 4
				if partition {
					wantExtended = 2
				}
				if len(delta.Extended) != wantExtended {
					t.Fatal("private creation lacks actual local/inherited extended payloads", delta)
				}
				var retainedExtended []func()
				for key, fact := range delta.Extended {
					if fact == nil || f.s.Transactions.Catalog.Clone().Statistics[address].Extended[key] != fact {
						t.Fatal("private clone lost actual extended pointer")
					}
					retainedExtended = append(retainedExtended, retainMixedExtended(t, fact))
				}
				if f.s.Transactions.Catalog.Clone().Statistics[address].PrivateMetadata != delta.PrivateMetadata {
					t.Fatal("private metadata clone copied retained backing")
				}
				if strings.Contains(completion, "PREPARED") {
					enginetest.SessionSQL(t, f.s, "PREPARE TRANSACTION 'mixed_private'")
					entry := core.EnginePreparedRef(f.e).Lookup("mixed_private")
					if entry == nil || entry.Catalog.Statistics[address].PrivateMetadata != delta.PrivateMetadata || f.e.Storage.RelationMetadata[address] != nil {
						t.Fatal("prepared private object leaked or lost metadata")
					}
					for key, fact := range delta.Extended {
						if entry.Catalog.Statistics[address].Extended[key] != fact {
							t.Fatal("prepared creation lost private extended backing")
						}
					}
					enginetest.SessionSQL(t, enginetest.StartSession(t, f.e, nil), completion)
				} else if completion == "savepoint" {
					enginetest.SessionSQL(t, f.s, "ROLLBACK TO before_creation")
					enginetest.SessionSQL(t, f.s, "COMMIT")
				} else {
					enginetest.SessionSQL(t, f.s, completion)
				}
				if strings.HasPrefix(completion, "COMMIT") {
					metadata := f.e.Storage.RelationMetadata[address]
					if metadata == nil || metadata.InheritedTuples != wantPopulation || metadata.AnalyzeCount != 1 {
						t.Fatal("committed creation lost real private maintenance")
					}
					for key, fact := range delta.Columns {
						if metadata.Statistics.Columns[key] != fact {
							t.Fatal("creation commit copied selected backing")
						}
					}
					for key, fact := range delta.Extended {
						if metadata.Statistics.Extended[key] != fact {
							t.Fatal("creation commit copied extended backing")
						}
					}
					if partition && f.e.Storage.Relations[address] != nil {
						t.Fatal("private partition commit manufactured heap")
					}
				} else if f.e.Storage.RelationMetadata[address] != nil || f.e.Storage.Relations[address] != nil {
					t.Fatal("aborted private mixed object left ghost")
				}
				for _, check := range retainedExtended {
					check()
				}
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeInheritedForeignPrivatePassFailure(t *testing.T) {
	for _, partition := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "partition"}[partition], func(t *testing.T) {
			f := newInheritedForeignFixture(t, 0, false)
			for _, q := range []string{"BEGIN", "CREATE TABLE private_failed(a integer,b integer)", "INSERT INTO private_failed VALUES(1,10)", "CREATE FOREIGN TABLE private_failed_foreign(a integer,b integer) INHERITS(private_failed) SERVER mixed_owner_server"} {
				enginetest.SessionSQL(t, f.s, q)
			}
			// Use a real no-local root in the partition branch, not a forged heap.
			name := "private_failed"
			if partition {
				for _, q := range []string{"CREATE TABLE private_failed_partition(a integer,b integer) PARTITION BY RANGE(a)", "CREATE FOREIGN TABLE private_partition_foreign(a integer,b integer) SERVER mixed_owner_server", "ALTER TABLE private_failed_partition ATTACH PARTITION private_partition_foreign FOR VALUES FROM(0) TO(4)"} {
					enginetest.SessionSQL(t, f.s, q)
				}
				name = "private_failed_partition"
			}
			sentinel := errors.New("private inherited pass rejected")
			f.wrapper.sources["private_failed_foreign"] = mixedForeignSource(1, 2, 30)
			f.wrapper.sources["private_partition_foreign"] = mixedForeignSource(1, 2, 30)
			f.wrapper.hook = func(context.Context, string, string) error { return sentinel }
			var address ids.ObjectAddress
			for a, d := range f.s.Transactions.Catalog.Descriptors {
				if r, ok := d.(*catalog.Relation); ok && r.Name() == name {
					address = a
				}
			}
			if err := f.execute(t, context.Background(), "ANALYZE ONLY "+name, false); !errors.Is(err, sentinel) {
				t.Fatal("missing genuine inherited failure", err)
			}
			if f.e.Storage.RelationMetadata[address] != nil || f.e.Storage.Relations[address] != nil {
				t.Fatal("failed private local pass published ghost metadata")
			}
			enginetest.SessionSQL(t, f.s, "ROLLBACK")
			if f.e.Storage.RelationMetadata[address] != nil || f.e.Storage.Relations[address] != nil {
				t.Fatal("rollback retained failed private maintenance")
			}
			enginetest.LockEmpty(t, f.e)
		})
	}
}
