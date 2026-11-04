package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Exercise the owning ANALYZE path, not just its eligibility helper. The
// unsupported-provider branch keeps the inherited collector genuinely native.
func TestAnalyzeInheritedForeignExtendedEligibility(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "mixed", true: "all_native"}[native]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				f := newInheritedForeignFixture(t, 10, false)
				source := mixedForeignSource(1, 30, 30)
				f.wrapper.sources["mixed_foreign"] = source
				if native {
					source.unsupported = true
					enginetest.SessionSQL(t, f.s, "CREATE TABLE mixed_exact(a integer,b integer) INHERITS(mixed_parent)")
					enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_exact VALUES"+strings.TrimSuffix(strings.Repeat("(2,20),", 30), ","))
				}
				for _, q := range []string{
					"CREATE STATISTICS mixed_eligible(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent",
					"ALTER STATISTICS mixed_eligible SET STATISTICS 2",
				} {
					enginetest.SessionSQL(t, f.s, q)
				}
				f.analyze(t, prepared, "")
				key := ndistinctKey(t, f.e, "mixed_eligible")
				key.Inherited = true
				baseline := enginetest.AnalyzeFacts(f.e, f.parent.Address)
				joint := baseline.Extended[key]
				if joint == nil || len(joint.Items) != 1 || joint.Items[0].Value != 2 {
					t.Fatal("baseline is not two genuine joint groups", joint)
				}
				retained := f.retained(t)
				enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_parent VALUES(1,11)")
				if native {
					enginetest.SessionSQL(t, f.s, "UPDATE mixed_exact SET b=21")
					enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_exact VALUES(2,22)")
				} else {
					for i, row := range source.sample.Rows {
						row[0] = int64(21 + i%2)
					}
				}
				// Four actual groups now differ from the retained two. Disabling
				// required a must not charge the object's target2 to mixed K.
				enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_parent ALTER a SET STATISTICS 0")
				source.targets = nil
				notices, err := f.executeNotices(t, context.Background(), "ANALYZE ONLY mixed_parent", prepared)
				if err != nil {
					t.Fatal(err)
				}
				want := []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: `statistics object "public.mixed_eligible" could not be computed for relation "public.mixed_parent"`, Schema: "public", Table: "mixed_parent"}}
				if !reflect.DeepEqual(notices, append(want, want[0])) {
					t.Fatal("local and inherited missing-simple-key diagnostics changed", notices)
				}
				after := enginetest.AnalyzeFacts(f.e, f.parent.Address)
				for k, fact := range baseline.Extended {
					if after.Extended[k] != fact {
						t.Fatal("disabled simple key rebuilt retained local/inherited payload", k)
					}
				}
				for k, fact := range baseline.Columns {
					if k.Attribute == f.e.CatalogRead().MustRelation(f.parent.Address).Attributes[0] && after.Columns[k] != fact {
						t.Fatal("disabled scalar backing replaced", k)
					}
				}
				if !native && (len(source.targets) != 1 || source.targets[0] != 150 || f.inherited(t, 1).Rows != 41) {
					t.Fatal("skipped object target inflated K300", source.targets)
				}
				if native && len(source.targets) != 0 {
					t.Fatal("all-native path acquired unsupported foreign records")
				}
				retained()
				enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_parent ALTER a SET STATISTICS 1")
				source.targets = nil
				f.analyze(t, prepared, "")
				fresh := enginetest.AnalyzeFacts(f.e, f.parent.Address).Extended[key]
				if fresh == joint || len(fresh.Items) != 1 || fresh.Items[0].Value != 4 || fresh.MCVData == nil || fresh.MCVData.Rows != map[bool]uint64{false: 41, true: 42}[native] {
					t.Fatal("reenabling key did not recompute four actual joint groups", fresh)
				}
				if !native && (len(source.targets) != 1 || source.targets[0] != 300) {
					t.Fatal("admitted target2 failed to raise K600", source.targets)
				}
				retained()

				// Expression-only definitions may read disabled a, but still
				// require a genuinely admitted selected scalar (b here).
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_expression ON (a+b) FROM mixed_parent")
				f.analyze(t, prepared, "")
				expressionKey := ndistinctKey(t, f.e, "mixed_expression")
				expressionKey.Inherited = true
				expressionBaseline := enginetest.AnalyzeFacts(f.e, f.parent.Address)
				expression := expressionBaseline.Extended[expressionKey]
				retainedExpression := f.retained(t)
				for _, q := range []string{"ALTER TABLE mixed_parent ALTER a SET STATISTICS 0", "ALTER TABLE mixed_parent ALTER b SET STATISTICS 0", "UPDATE ONLY mixed_parent SET b=99"} {
					enginetest.SessionSQL(t, f.s, q)
				}
				if native {
					enginetest.SessionSQL(t, f.s, "UPDATE mixed_exact SET b=98")
				} else {
					for _, row := range source.sample.Rows {
						row[0] = int64(98)
					}
				}
				source.targets = nil
				notices, err = f.executeNotices(t, context.Background(), "ANALYZE ONLY mixed_parent", prepared)
				if err != nil || len(notices) != 0 {
					t.Fatal("empty scalar admission should silently retain all extended objects", notices, err)
				}
				allDisabled := enginetest.AnalyzeFacts(f.e, f.parent.Address)
				for k, fact := range expressionBaseline.Extended {
					if allDisabled.Extended[k] != fact {
						t.Fatal("empty admission rebuilt local/inherited expression or joint", k)
					}
				}
				for k, fact := range expressionBaseline.Columns {
					if allDisabled.Columns[k] != fact {
						t.Fatal("empty admission rebuilt scalar", k)
					}
				}
				if !native && (len(source.targets) != 1 || source.targets[0] != 50) {
					t.Fatal("ineligible expression still charged K30000 instead of minimum100", source.targets)
				}
				retainedExpression()
				enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_parent ALTER b SET STATISTICS 1")
				source.targets = nil
				f.analyze(t, prepared, "(b)")
				positive := enginetest.AnalyzeFacts(f.e, f.parent.Address).Extended[expressionKey]
				if positive == expression || positive.Expression(0) == expression.Expression(0) || positive.Expression(0).DistinctCount != 1 || mixedMCVCount(t, positive.Expression(0), 100) != map[bool]uint64{false: 41, true: 42}[native] {
					t.Fatal("enabled selected b failed to rebuild actual changed expression values", positive)
				}
				if !native && (len(source.targets) != 1 || source.targets[0] != 15000) {
					t.Fatal("eligible expression did not raise K30000", source.targets)
				}
				retainedExpression()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeInheritedForeignExtendedEligibilityJournal(t *testing.T) {
	f := newInheritedForeignFixture(t, 10, false)
	f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
	enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_eligible_journal(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent")
	f.analyze(t, false, "")
	baseline := enginetest.AnalyzeFacts(f.e, f.parent.Address)
	retained := f.retained(t)
	enginetest.SessionSQL(t, f.s, "BEGIN")
	enginetest.SessionSQL(t, f.s, "SAVEPOINT admitted")
	enginetest.SessionSQL(t, f.s, "UPDATE ONLY mixed_parent SET b=11")
	f.analyze(t, true, "")
	first := f.s.Transactions.Catalog
	delta := first.Statistics[f.parent.Address]
	key := ndistinctKey(t, f.e, "mixed_eligible_journal")
	key.Inherited = true
	selected := delta.Extended[key]
	if selected == nil || selected == baseline.Extended[key] || enginetest.AnalyzeFacts(f.e, f.parent.Address) != baseline {
		t.Fatal("successful extended products not genuinely Journal-private")
	}
	keep := retainMixedExtended(t, selected)
	enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_parent ALTER a SET STATISTICS 0")
	enginetest.SessionSQL(t, f.s, "UPDATE ONLY mixed_parent SET b=12")
	f.analyze(t, false, "")
	current := f.s.Transactions.Catalog
	if current.Statistics[f.parent.Address].Extended[key] != selected || current.Clone().Statistics[f.parent.Address].Extended[key] != selected || first.Statistics[f.parent.Address].Extended[key] != selected {
		t.Fatal("skip rebuilt or dropped earlier immutable Journal delta")
	}
	relation := current.Statistics[f.parent.Address].Relation
	resolved := session.ResolveStatistics(current, f.parent.Address, f.e.Storage.RelationMetadata[f.parent.Address], relation, f.e.CatalogRead())
	if resolved.Extended[key] != selected {
		t.Fatal("resolver lost retained owning Journal payload")
	}
	keep()
	enginetest.SessionSQL(t, f.s, "SAVEPOINT skipped")
	maintenance := *f.e.Storage.RelationMetadata[f.parent.Address]
	failure := errors.New("later inherited failure with prior extended Journal")
	f.wrapper.sources["mixed_foreign"].failure = failure
	if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_parent(b)", true); !errors.Is(err, failure) {
		t.Fatal("later inherited failure missing", err)
	}
	failed := f.s.Transactions.Catalog.Statistics[f.parent.Address]
	if failed.Extended[key] != selected {
		t.Fatal("failed inherited target discarded earlier successful extended Journal")
	}
	for k, fact := range current.Statistics[f.parent.Address].Columns {
		if failed.Columns[k] != fact {
			t.Fatal("failed target merged its command-local scalar products", k)
		}
	}
	afterFailure := f.e.Storage.RelationMetadata[f.parent.Address]
	if afterFailure.Statistics != baseline || afterFailure.InheritedTuples != maintenance.InheritedTuples || afterFailure.AnalyzeCount != maintenance.AnalyzeCount+1 {
		t.Fatal("failed target changed typed/inherited facts or retracted completed local pass")
	}
	enginetest.SessionSQL(t, f.s, "ROLLBACK TO skipped")
	if f.s.Transactions.Catalog.Statistics[f.parent.Address].Extended[key] != selected {
		t.Fatal("rollback-to lost previous retained extended Journal")
	}
	keep()
	enginetest.SessionSQL(t, f.s, "ROLLBACK TO admitted")
	if enginetest.AnalyzeFacts(f.e, f.parent.Address) != baseline {
		t.Fatal("savepoint leaked typed payload")
	}
	if journal := f.s.Transactions.Catalog; journal != nil {
		if change, ok := journal.Statistics[f.parent.Address]; ok && len(change.Extended) != 0 {
			t.Fatal("savepoint retained discarded extended delta")
		}
	}
	enginetest.SessionSQL(t, f.s, "ROLLBACK")
	keep()
	retained()
	enginetest.LockEmpty(t, f.e)
}
