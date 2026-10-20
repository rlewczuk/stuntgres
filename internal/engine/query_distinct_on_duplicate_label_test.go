package engine

import (
	"stuntgres/internal/engine/plan"
	"testing"
)

func TestQueryDistinctOnDuplicateSubqueryLabelFirstOccurrence(t *testing.T) {
	for _, suffix := range []string{"", " ORDER BY x"} {
		_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(x) (SELECT max(v) FROM t) AS x,(SELECT max(t.v) FROM t) AS x FROM t"+suffix)
		defer source.close()
		project := query.Root.(*plan.ProjectNode)
		on := project.Child.(*plan.DistinctOnNode)
		first := project.Expressions[0].(plan.CachedOrderExpr)
		second := project.Expressions[1].(plan.CachedOrderExpr)
		key := on.Expressions[0].(plan.CachedOrderExpr)
		if first.Slot != key.Slot || second.Slot == key.Slot {
			t.Fatal("duplicate label did not retain first output occurrence", suffix, first, second, key)
		}
		if suffix != "" {
			sorted := on.Child.(*plan.SortNode)
			if sorted.Keys[0].OutputOrdinal != 1 || sorted.Keys[0].TargetIdentity != 1 {
				t.Fatal("ORDER duplicate label changed analyzed target occurrence", sorted.Keys)
			}
		}
	}
}

func TestQueryOrderDuplicateSubqueryLabelFirstOccurrence(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT (SELECT max(v) FROM t) AS x,(SELECT max(t.v) FROM t) AS x FROM t ORDER BY x")
	defer source.close()
	project := query.Root.(*plan.ProjectNode)
	sorted := project.Child.(*plan.SortNode)
	first := project.Expressions[0].(plan.CachedOrderExpr)
	second := project.Expressions[1].(plan.CachedOrderExpr)
	if sorted.Keys[0].OutputOrdinal != 1 || sorted.Keys[0].Cache != first.Slot || sorted.Keys[0].Cache == second.Slot {
		t.Fatal("ordinary ORDER duplicate label changed first output/cache occurrence", sorted.Keys, first, second)
	}
	if len(project.Schema()) != 2 || project.Schema()[0].Name != "x" || project.Schema()[1].Name != "x" {
		t.Fatal("ordinary ORDER changed visible duplicate output metadata", project.Schema())
	}
}
