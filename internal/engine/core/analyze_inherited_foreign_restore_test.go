package core_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Distinct-source replacement restores actual typed snapshots. Same-owner
// replacement deliberately discards persistent facts and is tested separately.
func TestAnalyzeInheritedForeignRestoredOwnership(t *testing.T) {
	for _, partition := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "partition"}[partition], func(t *testing.T) {
			source := newInheritedForeignFixture(t, 10, partition)
			source.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 990.5)
			for _, q := range []string{
				"ALTER TABLE mixed_parent ALTER a SET STATISTICS 2",
				"ALTER TABLE mixed_parent ALTER b SET STATISTICS 2",
				"CREATE STATISTICS mixed_restored_joint(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent",
				"CREATE STATISTICS mixed_restored_expression ON (a+b) FROM mixed_parent",
				"CREATE SEQUENCE mixed_restore_sequence",
				"CREATE STATISTICS mixed_restored_sampled_distinct ON (nextval('mixed_restore_sequence') % 34) FROM mixed_parent",
			} {
				enginetest.SessionSQL(t, source.s, q)
			}
			source.analyze(t, true, "")
			before := source.e.Storage.RelationMetadata[source.parent.Address]
			original := before.Statistics
			retained := source.retained(t)
			if before.InheritedTuples != 1000.5 || source.inherited(t, 0).Rows != 40 || mixedMCVCount(t, source.inherited(t, 0), 1) != 10 || mixedMCVCount(t, source.inherited(t, 0), 2) != 30 {
				t.Fatal("source lacks genuine40-record sample separate from acquired1000.5 population")
			}
			// Any40 consecutive sequence values modulo34 contain six
			// doubletons and28 singletons. With acquired population1000.5,
			// the distinct estimate is round(1360/(12+1120/1000.5))=104,
			// encoded -104/1000.5, not the measured34/40 encoding -.85.
			wantSampledDistinct := -104.0 / 1000.5
			checkSampledDistinct := func(expression *storage.ColumnStatistics) {
				t.Helper()
				encoded, known := expression.EncodedDistinct()
				if expression.Rows != 40 || expression.NullCount != 0 || expression.DistinctCount != 34 || expression.DistinctOverride != wantSampledDistinct || !known || encoded != wantSampledDistinct || encoded == -34.0/40 || len(expression.MCV) != 6 || len(expression.Histogram) != 28 {
					t.Fatal("sampled expression lost independently known distinct estimate/counts", expression, encoded, known)
				}
				for _, item := range expression.MCV {
					if item.Count != 2 {
						t.Fatal("sampled modulo expression lost genuine doubleton count", item)
					}
				}
			}
			sampledKey := ndistinctKey(t, source.e, "mixed_restored_sampled_distinct")
			sampledKey.Inherited = true
			checkSampledDistinct(original.Extended[sampledKey].Expression(0))
			// Real source changes must not be used to repair stale snapshots:
			// imported native data has11 rows; retained sample still has40.
			enginetest.SessionSQL(t, source.s, "INSERT INTO mixed_parent VALUES(1,11)")
			for _, row := range source.wrapper.sources["mixed_foreign"].sample.Rows {
				row[0], row[1] = int64(99), int64(3)
			}
			target := newInheritedForeignFixture(t, 0, partition)
			sourceCalls, targetCalls := len(source.wrapper.negotiated), len(target.wrapper.negotiated)
			rejectProvider := func(context.Context, string, string) error {
				t.Fatal("typed native replacement/consumer negotiated foreign provider")
				return nil
			}
			source.wrapper.hook, target.wrapper.hook = rejectProvider, rejectProvider
			if _, err := target.e.ReplaceCatalog(context.Background(), source.e, source.e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			read := target.e.CatalogRead()
			parent := enginetest.CatalogRef(t, target.e, read, "mixed_parent")
			foreign := enginetest.CatalogRef(t, target.e, read, "mixed_foreign")
			root := read.MustRelation(parent.Address)
			metadata := target.e.Storage.RelationMetadata[parent.Address]
			if parent.Domain() == source.parent.Domain() || root == source.e.CatalogRead().MustRelation(source.parent.Address) || metadata == nil || metadata == before || metadata.Statistics == nil || metadata.Statistics == original {
				t.Fatal("distinct replacement borrowed source catalog/statistics authority")
			}
			// Typed snapshot import is not completed ANALYZE maintenance. These
			// existing snapshots do not transport acquired population/count/time.
			if metadata.InheritedScanned || metadata.InheritedTuples != 0 || metadata.Scanned || metadata.Tuples != 0 || metadata.AnalyzeCount != 0 || !metadata.LastAnalyze.IsZero() {
				t.Fatal("typed snapshot restore fabricated completed maintenance/population", metadata)
			}
			native := parent
			if partition {
				native = enginetest.CatalogRef(t, target.e, read, "mixed_native")
			}
			if heap := target.e.Storage.Relations[native.Address]; heap == nil || heap.Maintenance.Scanned || heap.Maintenance.Tuples != 0 {
				t.Fatal("typed restore fabricated a native heap scan")
			}
			facts := metadata.Statistics
			if len(facts.Columns) != len(original.Columns) || len(facts.Extended) != len(original.Extended) {
				t.Fatal("restore lost scalar or extended local/inherited flavors")
			}
			checkHierarchy := func(hierarchy *storage.StatisticsHierarchy) {
				t.Helper()
				want := 2
				if partition {
					want = 3
				}
				if hierarchy == nil || len(hierarchy.Relations) != want || !storage.StatisticsHierarchyCurrent(hierarchy, root, read) {
					t.Fatal("restored full foreign hierarchy is missing or stale", hierarchy)
				}
				seenForeign := false
				for _, relation := range hierarchy.Relations {
					if relation != read.MustRelation(relation.Address()) {
						t.Fatal("restored hierarchy retained a source descriptor")
					}
					if relation.Address() == foreign.Address {
						seenForeign = relation.Foreign != nil
					}
				}
				if !seenForeign {
					t.Fatal("typed restore pruned actual foreign structural member")
				}
			}
			for key, old := range original.Columns {
				fact := facts.Columns[key]
				if fact == nil || fact == old || fact.Usage != root.Columns[root.IndexForAttribute(key.Attribute)].Usage || fact.Rows != old.Rows || fact.NullCount != old.NullCount || fact.DistinctCount != old.DistinctCount || fact.DistinctKnown != old.DistinctKnown || fact.DistinctOverride != old.DistinctOverride {
					t.Fatal("scalar typed remint lost stale retained sample/estimated distinct facts", key)
				}
				if key.Inherited {
					if fact.Rows != 40 || mixedMCVCount(t, fact, map[int32]int64{root.Attributes[0]: 1, root.Attributes[1]: 10}[key.Attribute]) != 10 || mixedMCVCount(t, fact, map[int32]int64{root.Attributes[0]: 2, root.Attributes[1]: 20}[key.Attribute]) != 30 {
						t.Fatal("restore recomputed stale40-record scalar sample from changed live/provider rows", key, fact)
					}
					checkHierarchy(fact.Hierarchy)
				}
			}
			// Expression-only collection leaves Items nil; snapshot restore
			// constructs make([]StatisticsItem, 0). Compare construction
			// members, not the unencoded nil/empty slice representation.
			sameItems := func(left, right []sqltype.StatisticsItem) bool {
				return slices.EqualFunc(left, right, func(a, b sqltype.StatisticsItem) bool {
					return a.Value == b.Value && slices.Equal(a.Attributes, b.Attributes)
				})
			}
			for oldKey, old := range original.Extended {
				name := source.e.CatalogRead().Get(oldKey.Definition.Address).Name()
				key := ndistinctKey(t, target.e, name)
				key.Inherited = oldKey.Inherited
				definition, ok := read.Statistics(key.Definition.Address)
				fact := facts.Extended[key]
				if !ok {
					t.Fatal("restored extended definition missing", name)
				}
				if key.Definition.Domain() != read.Domain() || key.Definition.Domain() == oldKey.Definition.Domain() {
					t.Fatal("restored extended key did not gain destination identity", name, oldKey.Inherited)
				}
				if fact == nil || fact == old {
					t.Fatal("restored extended carrier missing or borrowed from source", name, oldKey.Inherited)
				}
				if fact.Relation != definition.Relation || fact.Relation != parent || fact.Selection != definition.Selection || fact.Recipes != definition.Expressions {
					t.Fatal("restored extended carrier has wrong current relation/selection/recipe authority", name, oldKey.Inherited)
				}
				if !sameItems(fact.Items, old.Items) || !sameItems(fact.DependencyItems, old.DependencyItems) {
					t.Fatal("restored joint/dependency construction members changed", name, oldKey.Inherited)
				}
				if fact.ExpressionLen() != old.ExpressionLen() {
					t.Fatal("restored expression summaries missing", name, oldKey.Inherited)
				}
				if old.MCVData != nil {
					if fact.MCVData == nil || fact.MCVData == old.MCVData || fact.MCVData.Rows != old.MCVData.Rows || !reflect.DeepEqual(fact.MCVData.Items, old.MCVData.Items) {
						t.Fatal("restored MCV sample construction counts changed or borrowed source backing")
					}
					if key.Inherited && (fact.MCVData.Rows != 40 || len(fact.Items) != 1 || fact.Items[0].Value != 2 || len(fact.DependencyItems) != 2) {
						t.Fatal("restored joint sample lost independently known40 rows/two groups/dependencies")
					}
				}
				for i := range old.ExpressionLen() {
					expression := fact.Expression(i)
					if expression == old.Expression(i) || expression.Usage != definition.Expressions.At(i).Usage() || expression.Rows != old.Expression(i).Rows || expression.DistinctOverride != old.Expression(i).DistinctOverride {
						t.Fatal("expression sample authority not reowned")
					}
					if key.Inherited {
						if name == "mixed_restored_sampled_distinct" {
							checkSampledDistinct(expression)
						} else if expression.Rows != 40 || expression.DistinctCount != 2 || mixedMCVCount(t, expression, 11) != 10 || mixedMCVCount(t, expression, 22) != 30 {
							t.Fatal("restored expression remeasured changed source values")
						}
					}
				}
				if key.Inherited {
					checkHierarchy(fact.Hierarchy)
				}
			}
			if storage.CompatibleExtendedStatistics(facts, root, read) != facts {
				t.Fatal("restored definition/provider authority rejected actual reminted facts")
			}
			enginetest.TransactionRows(t, target.s, "SELECT count(*) FROM ONLY mixed_parent", map[bool]int64{false: 11, true: 0}[partition])
			if partition {
				enginetest.TransactionRows(t, target.s, "SELECT count(*) FROM mixed_native", 11)
			}
			enginetest.SessionSQL(t, target.s, "EXPLAIN SELECT * FROM mixed_parent WHERE a=2")
			if len(source.wrapper.negotiated) != sourceCalls || len(target.wrapper.negotiated) != targetCalls || source.wrapper.active.Load() != 0 || target.wrapper.active.Load() != 0 {
				t.Fatal("restore/current consumers reacquired providers or leaked sample work")
			}
			if target.e.Storage.Relations[foreign.Address] != nil || partition && target.e.Storage.Relations[parent.Address] != nil {
				t.Fatal("native typed restore fabricated foreign/partition heap")
			}
			retained()
			enginetest.LockEmpty(t, source.e)
			enginetest.LockEmpty(t, target.e)
		})
	}
}
