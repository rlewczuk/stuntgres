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
	"stuntgres/internal/sqlstate"
)

func TestAnalyzeForeignExtendedJournalOwnership(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'direct_extended'", "ROLLBACK PREPARED 'direct_extended'"} {
		t.Run(completion, func(t *testing.T) {
			f := newDirectExtendedFixture(t)
			directAnalyze(t, f, false, "")
			baseline := enginetest.AnalyzeFacts(f.e, f.foreign.Address)
			retained := f.retained(t)
			relation := f.e.CatalogRead().MustRelation(f.foreign.Address)
			a, b := storage.StatisticsKey{Attribute: relation.Attributes[1]}, storage.StatisticsKey{Attribute: relation.Attributes[0]}
			jointKey, expressionKey := ndistinctKey(t, f.e, "direct_joint"), ndistinctKey(t, f.e, "direct_expression")
			peer := f
			peer.s = enginetest.StartSession(t, f.e, nil)
			enginetest.SessionSQL(t, f.s, "BEGIN")
			directAnalyze(t, f, true, "(a)")
			first := f.s.Transactions.Catalog
			delta := first.Statistics[f.foreign.Address]
			selected := delta.Extended[expressionKey]
			if delta.Original != nil || delta.OriginalRelation != relation || delta.Relation != relation || len(delta.Columns) != 1 || delta.Columns[a] == nil || delta.Columns[a] == baseline.Columns[a] || len(delta.Extended) != 1 || selected == nil || selected == baseline.Extended[expressionKey] || enginetest.AnalyzeFacts(f.e, f.foreign.Address) != baseline {
				t.Fatal("actual direct selected facts did not transfer into owning Journal", delta)
			}
			clone := first.Clone()
			if clone == first || clone.Statistics[f.foreign.Address].Extended[expressionKey] != selected || clone.Statistics[f.foreign.Address].Columns[a] != delta.Columns[a] {
				t.Fatal("Journal.Clone copied or lost immutable direct backing")
			}
			keep := retainMixedExtended(t, selected)
			enginetest.SessionSQL(t, f.s, "SAVEPOINT kept")
			directAnalyze(t, f, false, "(b)")
			latest := f.s.Transactions.Catalog.Statistics[f.foreign.Address]
			if len(latest.Columns) != 2 || latest.Columns[a] != delta.Columns[a] || latest.Extended[expressionKey] == selected || first.Statistics[f.foreign.Address].Extended[expressionKey] != selected || len(first.Statistics[f.foreign.Address].Columns) != 1 {
				t.Fatal("later selected merge mutated predecessor instead of replacing owning delta")
			}
			enginetest.SessionSQL(t, f.s, "ROLLBACK TO kept")
			own := session.ResolveStatistics(f.s.Transactions.Catalog, f.foreign.Address, f.e.Storage.RelationMetadata[f.foreign.Address], relation, f.e.CatalogRead())
			if own.Columns[a] != delta.Columns[a] || own.Columns[b] != baseline.Columns[b] || own.Extended[expressionKey] != selected || own.Extended[jointKey] != baseline.Extended[jointKey] {
				t.Fatal("savepoint/resolver lost selected or unselected exact backing")
			}
			// A later canonical refresh of an unrelated scalar must survive this
			// transaction's completion; its expression competes as a selected key.
			directAnalyze(t, peer, false, "(b)")
			peerFacts := enginetest.AnalyzeFacts(f.e, f.foreign.Address)
			newB, peerExpression := peerFacts.Columns[b], peerFacts.Extended[expressionKey]
			if newB == baseline.Columns[b] || peerFacts.Extended[jointKey] != baseline.Extended[jointKey] {
				t.Fatal("peer failed to retain ineligible joint while refreshing selected b")
			}
			maintenance := *f.e.Storage.RelationMetadata[f.foreign.Address]
			rows, versions := f.e.Storage.RowFrontier(), f.e.Storage.VersionFrontier()
			if strings.Contains(completion, "PREPARED") {
				enginetest.SessionSQL(t, f.s, "PREPARE TRANSACTION 'direct_extended'")
				entry := core.EnginePreparedRef(f.e).Lookup("direct_extended")
				if entry == nil || entry.Catalog.Statistics[f.foreign.Address].Extended[expressionKey] != selected || entry.Catalog.Statistics[f.foreign.Address].Columns[a] != delta.Columns[a] || f.s.Transactions.Catalog != nil {
					t.Fatal("PREPARE lost genuine selected owner")
				}
				enginetest.SessionSQL(t, peer.s, completion)
				if core.EnginePreparedRef(f.e).Lookup("direct_extended") != nil {
					t.Fatal("completed prepared statistics retained ownership")
				}
			} else {
				enginetest.SessionSQL(t, f.s, completion)
			}
			wantA, wantExpression := baseline.Columns[a], peerExpression
			if strings.HasPrefix(completion, "COMMIT") {
				wantA, wantExpression = delta.Columns[a], selected
			}
			after := *f.e.Storage.RelationMetadata[f.foreign.Address]
			if after.Statistics.Columns[a] != wantA || after.Statistics.Columns[b] != newB || after.Statistics.Extended[expressionKey] != wantExpression || after.Statistics.Extended[jointKey] != baseline.Extended[jointKey] {
				t.Fatal("completion lost selected delta or newer unselected canonical backing")
			}
			after.Statistics = maintenance.Statistics
			if after != maintenance || f.e.Storage.Relations[f.foreign.Address] != nil || f.e.Storage.RowFrontier() != rows || f.e.Storage.VersionFrontier() != versions {
				t.Fatal("typed completion replayed maintenance or created physical rows")
			}
			keep()
			retained()
			enginetest.LockEmpty(t, f.e)
		})
	}
}

func TestAnalyzeForeignExtendedPrivateOwnership(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK", "savepoint", "COMMIT PREPARED 'direct_private'", "ROLLBACK PREPARED 'direct_private'"} {
		t.Run(completion, func(t *testing.T) {
			f := newInheritedForeignFixture(t, 0, false)
			f.wrapper.sources["direct_private"] = &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), int64(20)}}, TotalRows: 30.5, TotalPages: 2}}
			for _, q := range []string{
				"BEGIN", "SAVEPOINT before_creation",
				"CREATE FOREIGN TABLE direct_private(a integer,b integer) SERVER mixed_owner_server",
				"CREATE STATISTICS direct_private_joint(ndistinct,dependencies,mcv) ON a,b FROM direct_private",
				"CREATE STATISTICS direct_private_expression ON (a+b) FROM direct_private",
				"ANALYZE direct_private",
			} {
				enginetest.SessionSQL(t, f.s, q)
			}
			var address ids.ObjectAddress
			var delta session.StatisticsChange
			for a, change := range f.s.Transactions.Catalog.Statistics {
				if change.Relation.Name() == "direct_private" {
					address, delta = a, change
				}
			}
			if delta.PrivateMetadata == nil || delta.PrivateMetadata.AnalyzeCount != 1 || delta.PrivateMetadata.ForeignAnalyze == nil || delta.PrivateMetadata.ForeignAnalyze.SampleRows != 3 || delta.PrivateMetadata.ForeignAnalyze.TotalRows != 30.5 || delta.Original != nil || delta.OriginalRelation != nil || len(delta.Columns) != 2 || len(delta.Extended) != 2 || f.e.Storage.RelationMetadata[address] != nil || f.e.Storage.Relations[address] != nil {
				t.Fatal("private direct foreign creation lacks real isolated scalar/extended ownership", delta)
			}
			clone := f.s.Transactions.Catalog.Clone()
			if clone.Statistics[address].PrivateMetadata != delta.PrivateMetadata {
				t.Fatal("private metadata cloned instead of shared immutably")
			}
			var retained []func()
			for key, fact := range delta.Extended {
				if key.Inherited || fact == nil || fact.Hierarchy != nil || clone.Statistics[address].Extended[key] != fact {
					t.Fatal("private extended backing lost actual local flavor", key)
				}
				retained = append(retained, retainMixedExtended(t, fact))
			}
			if strings.Contains(completion, "PREPARED") {
				enginetest.SessionSQL(t, f.s, "PREPARE TRANSACTION 'direct_private'")
				entry := core.EnginePreparedRef(f.e).Lookup("direct_private")
				if entry == nil || entry.Catalog.Statistics[address].PrivateMetadata != delta.PrivateMetadata || f.e.Storage.RelationMetadata[address] != nil {
					t.Fatal("prepared private direct relation leaked metadata")
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
				if metadata == nil || metadata.AnalyzeCount != 1 || metadata.ForeignAnalyze.TotalRows != 30.5 || len(metadata.Statistics.Extended) != 2 {
					t.Fatal("private direct commit lost real products/maintenance")
				}
				for key, fact := range delta.Extended {
					if metadata.Statistics.Extended[key] != fact {
						t.Fatal("private commit recopied accepted extended backing")
					}
				}
			} else if f.e.Storage.RelationMetadata[address] != nil {
				t.Fatal("aborted private direct creation left public ghost")
			}
			if f.e.Storage.Relations[address] != nil {
				t.Fatal("foreign private completion manufactured heap")
			}
			for _, check := range retained {
				check()
			}
			enginetest.LockEmpty(t, f.e)
		})
	}
}

func TestAnalyzeForeignExtendedPassSemantics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			f := newDirectExtendedFixture(t)
			for _, q := range []string{
				"CREATE TABLE direct_root_child(b integer,a integer)",
				"ALTER TABLE direct_root_child INHERIT mixed_foreign",
				"INSERT INTO direct_root_child VALUES(40,4),(40,4)",
				"CREATE STATISTICS direct_root_failure ON (100/b) FROM mixed_foreign",
			} {
				enginetest.SessionSQL(t, f.s, q)
			}
			directAnalyze(t, f, prepared, "")
			key := ndistinctKey(t, f.e, "direct_joint")
			inheritedKey := key
			inheritedKey.Inherited = true
			before := f.e.Storage.RelationMetadata[f.foreign.Address]
			directJoint(t, before.Statistics.Extended[key], 6, 3)
			inherited := before.Statistics.Extended[inheritedKey]
			if inherited == nil || len(inherited.Items) != 1 || inherited.Items[0].Value != 4 || inherited.MCVData.Rows != 8 || inherited.Hierarchy == nil || before.ForeignAnalyze.TotalRows != 6 || before.InheritedTuples != 8 || before.AnalyzeCount != 1 || len(before.Statistics.Extended) != 6 {
				t.Fatal("foreign root local and inherited products did not coexist", before)
			}
			retained := f.retained(t)
			enginetest.SessionSQL(t, f.s, "UPDATE ONLY direct_root_child SET b=0")
			f.wrapper.sources["mixed_foreign"].sample.TotalRows = 60.5
			err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
				t.Fatal("genuine later inherited expression did not fail", err)
			}
			after := f.e.Storage.RelationMetadata[f.foreign.Address]
			if after == before || after.ForeignAnalyze.TotalRows != 60.5 || after.ForeignAnalyze.SampleRows != 6 || after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || after.InheritedTuples != 8 || after.Statistics != before.Statistics {
				t.Fatal("failed inherited pass retracted completed direct maintenance or leaked typed products", after)
			}
			retained()
			enginetest.SessionSQL(t, f.s, "UPDATE ONLY direct_root_child SET b=40")
			directAnalyze(t, f, prepared, "")
			fresh := f.e.Storage.RelationMetadata[f.foreign.Address]
			if fresh.Statistics.Extended[key] == before.Statistics.Extended[key] || fresh.Statistics.Extended[inheritedKey] == inherited || fresh.InheritedTuples != 62.5 || fresh.AnalyzeCount != after.AnalyzeCount+1 {
				t.Fatal("successful retry failed to publish both real flavors once", fresh)
			}
			retained()
			enginetest.LockEmpty(t, f.e)
		})
	}
}
