package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func expressionHierarchyFixture(t *testing.T, partitioned bool) (*core.Engine, *session.Session, ids.ObjectRef, ids.ObjectRef, storage.ExtendedStatisticsKey) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	var statements []string
	if partitioned {
		statements = []string{
			"CREATE TABLE expr_hierarchy(a INT,b INT) PARTITION BY RANGE(a)",
			"CREATE TABLE expr_hierarchy_child PARTITION OF expr_hierarchy FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE expr_hierarchy_null PARTITION OF expr_hierarchy DEFAULT",
			"INSERT INTO expr_hierarchy VALUES(1,10),(1,10),(2,20),(2,20),(NULL,NULL),(NULL,NULL)",
		}
	} else {
		statements = []string{
			"CREATE TABLE expr_hierarchy(a INT,b INT)",
			"CREATE TABLE expr_hierarchy_child(noise INT,b INT,a INT)",
			"ALTER TABLE expr_hierarchy_child INHERIT expr_hierarchy",
			"INSERT INTO expr_hierarchy VALUES(1,10),(1,10),(NULL,NULL),(NULL,NULL)",
			"INSERT INTO expr_hierarchy_child(noise,b,a) VALUES(999,20,2),(999,20,2),(999,30,NULL),(999,30,NULL)",
		}
	}
	statements = append(statements,
		"CREATE STATISTICS expr_hierarchy_s(ndistinct) ON (a+1),b FROM expr_hierarchy",
		"ANALYZE ONLY expr_hierarchy",
	)
	for _, statement := range statements {
		enginetest.SessionSQL(t, s, statement)
	}
	parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "expr_hierarchy")
	child := enginetest.CatalogRef(t, e, e.CatalogRead(), "expr_hierarchy_child")
	return e, s, parent, child, ndistinctKey(t, e, "expr_hierarchy_s")
}

func expressionHierarchyFact(t *testing.T, facts *storage.RelationStatistics, key storage.ExtendedStatisticsKey, rows, nulls, distinct uint64, groups float64, values map[int64]uint64) *storage.ExtendedStatistics {
	t.Helper()
	if facts == nil || facts.Extended[key] == nil {
		t.Fatal("missing actual extended fact", key)
	}
	fact := facts.Extended[key]
	if fact.ExpressionLen() != 1 || len(fact.Items) != 1 || fact.Items[0].Value != groups {
		t.Fatal("missing joint/expression population", fact)
	}
	column := fact.Expression(0)
	if column.Rows != rows || column.NullCount != nulls || !column.DistinctKnown || column.DistinctCount != distinct || column.AverageWidth != 4 || len(column.MCV) != len(values) {
		t.Fatal("wrong captured expression population", column)
	}
	for _, mcv := range column.MCV {
		n, err := mcv.Value.Integer()
		if err != nil || values[n] != mcv.Count {
			t.Fatal("parent/child layout was treated as positional", n, mcv.Count, err)
		}
	}
	return fact
}

func TestStatisticsExpressionPublicationInheritedAuthority(t *testing.T) {
	e, s, parent, child, localKey := expressionHierarchyFixture(t, false)
	inheritedKey := localKey
	inheritedKey.Inherited = true
	baseline := analyzeFacts(e, parent.Address)
	local := expressionHierarchyFact(t, baseline, localKey, 4, 2, 1, 2, map[int64]uint64{2: 2})
	inherited := expressionHierarchyFact(t, baseline, inheritedKey, 8, 4, 2, 4, map[int64]uint64{2: 2, 3: 2})
	read := e.CatalogRead()
	relation := read.MustRelation(parent.Address)
	definition, _ := read.Statistics(localKey.Definition.Address)
	if local == inherited || local.Expression(0) == inherited.Expression(0) || local.Hierarchy != nil || inherited.Hierarchy == nil || len(inherited.Hierarchy.Relations) != 2 || !storage.StatisticsHierarchyCurrent(inherited.Hierarchy, relation, read) {
		t.Fatal("local/inherited ownership or hierarchy authority was shared incorrectly")
	}
	if local.Recipes != definition.Expressions || inherited.Recipes != definition.Expressions || analyzeFacts(e, child.Address) != nil {
		t.Fatal("ONLY lost retained recipe backing or refreshed the child separately")
	}
	childDescriptor := read.MustRelation(child.Address)
	if childDescriptor.IndexForAttribute(childDescriptor.Attributes[childDescriptor.Index["a"]]) == relation.Index["a"] {
		t.Fatal("fixture does not distinguish child and parent layouts")
	}
	enginetest.SessionSQL(t, s, "INSERT INTO expr_hierarchy_child(noise,b,a) VALUES(777,90,9)")
	stale := analyzeFacts(e, parent.Address)
	if stale.Extended[localKey] != local || stale.Extended[inheritedKey] != inherited {
		t.Fatal("DML invalidated retained local/inherited expression backing")
	}
	// A real captured child-usage replacement invalidates only the inherited
	// population; it cannot mutate or discard the unchanged local population.
	next := catalog.CloneState(e.Catalog.State)
	changed := *next.MustRelation(child.Address)
	changed.Columns = append([]catalog.Column(nil), changed.Columns...)
	integer, _ := read.LookupType(sqltype.Int8)
	changed.Columns[changed.Index["a"]].Usage = integer.Usage()
	changed.ObjectRevision = changed.Revision() + 1
	next.SetDescriptor(child.Address, &changed)
	filtered := storage.CompatibleExtendedStatistics(baseline, next.MustRelation(parent.Address), e.Catalog.Read(next))
	if filtered == nil || filtered.Extended[localKey] != local || filtered.Extended[inheritedKey] != nil || baseline.Extended[inheritedKey] != inherited {
		t.Fatal("child provider/layout replacement did not isolate inherited invalidation")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE expr_hierarchy_child NO INHERIT expr_hierarchy")
	retired := analyzeFacts(e, parent.Address)
	if retired == nil || retired.Extended[localKey] != local || retired.Extended[inheritedKey] != nil || storage.StatisticsHierarchyCurrent(inherited.Hierarchy, e.CatalogRead().MustRelation(parent.Address), e.CatalogRead()) {
		t.Fatal("retired child retained obsolete inherited authority or changed local backing")
	}
	if inherited.Expression(0).Rows != 8 || inherited.Hierarchy.Relations[1] != childDescriptor {
		t.Fatal("hierarchy retirement mutated an old captured population")
	}
}

func TestStatisticsExpressionSnapshotInheritedState(t *testing.T) {
	for _, partitioned := range []bool{false, true} {
		name := "ordinary"
		if partitioned {
			name = "partitioned"
		}
		t.Run(name, func(t *testing.T) {
			source, s, parent, _, localKey := expressionHierarchyFixture(t, partitioned)
			inheritedKey := localKey
			inheritedKey.Inherited = true
			before := analyzeFacts(source, parent.Address)
			if before == nil {
				t.Fatal("hierarchy collection did not publish canonical metadata")
			}
			var oldLocal *storage.ExtendedStatistics
			rows, nulls, groups := uint64(8), uint64(4), float64(4)
			if partitioned {
				rows, nulls, groups = 6, 2, 3
				if source.Storage.Relations[parent.Address] != nil || before.Extended[localKey] != nil {
					t.Fatal("partitioned parent gained synthetic local storage/facts")
				}
			} else {
				oldLocal = expressionHierarchyFact(t, before, localKey, 4, 2, 1, 2, map[int64]uint64{2: 2})
			}
			oldInherited := expressionHierarchyFact(t, before, inheritedKey, rows, nulls, 2, groups, map[int64]uint64{2: 2, 3: 2})
			if oldInherited.Hierarchy == nil {
				t.Fatal("inherited expression omitted hierarchy authority")
			}
			if partitioned {
				enginetest.SessionSQL(t, s, "INSERT INTO expr_hierarchy VALUES(3,30)")
			} else {
				enginetest.SessionSQL(t, s, "UPDATE expr_hierarchy SET a=9,b=90")
				enginetest.SessionSQL(t, s, "UPDATE expr_hierarchy_child SET a=9,b=90")
			}
			if analyzeFacts(source, parent.Address).Extended[inheritedKey] != oldInherited {
				t.Fatal("current rows replaced stale inherited backing before ANALYZE")
			}
			var dump strings.Builder
			if err := engine.Dump(context.Background(), source, &dump); err != nil {
				t.Fatal(err)
			}
			target := engine.New()
			if err := engine.Restore(context.Background(), target, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			read := target.CatalogRead()
			parentRef := enginetest.CatalogRef(t, target, read, "expr_hierarchy")
			restoredLocalKey := ndistinctKey(t, target, "expr_hierarchy_s")
			restoredInheritedKey := restoredLocalKey
			restoredInheritedKey.Inherited = true
			restoredRoot := analyzeFacts(target, parentRef.Address)
			restored := expressionHierarchyFact(t, restoredRoot, restoredInheritedKey, rows, nulls, 2, groups, map[int64]uint64{2: 2, 3: 2})
			if restored == oldInherited || restored.Expression(0) == oldInherited.Expression(0) || restored.Hierarchy == nil || restored.Hierarchy == oldInherited.Hierarchy || restored.Recipes == oldInherited.Recipes || !storage.StatisticsHierarchyCurrent(restored.Hierarchy, read.MustRelation(parentRef.Address), read) {
				t.Fatal("stale inherited facts were recomputed or retained foreign generation authority")
			}
			for _, captured := range restored.Hierarchy.Relations {
				if read.Get(captured.Address()) != captured {
					t.Fatal("restored hierarchy retained a source descriptor")
				}
			}
			definition, _ := read.Statistics(restoredInheritedKey.Definition.Address)
			if restored.Recipes != definition.Expressions || restoredInheritedKey.Definition.Domain() != read.Domain() || restoredInheritedKey.Definition.Epoch != read.Epoch() {
				t.Fatal("restored recipe or population belongs to a foreign authority")
			}
			if partitioned {
				if restoredRoot.Extended[restoredLocalKey] != nil || target.Storage.Relations[parentRef.Address] != nil || len(restored.Hierarchy.Relations) != 3 {
					t.Fatal("partition replay fabricated local population or lost a leaf")
				}
			} else {
				local := expressionHierarchyFact(t, restoredRoot, restoredLocalKey, 4, 2, 1, 2, map[int64]uint64{2: 2})
				if local == oldLocal || local.Hierarchy != nil || local.Expression(0) == restored.Expression(0) {
					t.Fatal("local replay adopted source backing or inherited authority")
				}
			}
			targetSession := enginetest.StartSession(t, target, nil)
			enginetest.SessionSQL(t, targetSession, "ANALYZE ONLY expr_hierarchy")
			fresh := analyzeFacts(target, parentRef.Address).Extended[restoredInheritedKey]
			if fresh == nil || fresh == restored || fresh.Hierarchy == restored.Hierarchy {
				t.Fatal("fresh scan did not acquire independent hierarchy backing")
			}
			if partitioned {
				if fresh.Expression(0).Rows != 7 || fresh.Expression(0).NullCount != 2 || fresh.Expression(0).DistinctCount != 3 || fresh.Items[0].Value != 4 {
					t.Fatal("partition fresh population did not differ from stale replay", fresh)
				}
			} else if fresh.Expression(0).Rows != 8 || fresh.Expression(0).NullCount != 0 || fresh.Expression(0).DistinctCount != 1 || fresh.Items[0].Value != 1 {
				t.Fatal("ordinary fresh population did not differ from stale replay", fresh)
			}
			if restored.Expression(0).Rows != rows || restored.Expression(0).NullCount != nulls || oldInherited.Expression(0).Rows != rows {
				t.Fatal("fresh collection mutated stale/source backing")
			}
		})
	}
}
