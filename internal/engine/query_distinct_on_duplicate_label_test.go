package engine

import "testing"

func TestQueryDistinctOnDuplicateSubqueryLabelFirstOccurrence(t *testing.T) {
	for _, suffix := range []string{"", " ORDER BY x"} {
		_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(x) (SELECT max(v) FROM t) AS x,(SELECT max(t.v) FROM t) AS x FROM t"+suffix)
		defer source.close()
		project := query.Root.(*projectNode)
		on := project.child.(*distinctOnNode)
		first := project.expressions[0].(cachedOrderExpression)
		second := project.expressions[1].(cachedOrderExpression)
		key := on.expressions[0].(cachedOrderExpression)
		if first.slot != key.slot || second.slot == key.slot {
			t.Fatal("duplicate label did not retain first output occurrence", suffix, first, second, key)
		}
		if suffix != "" {
			sorted := on.child.(*sortNode)
			if sorted.keys[0].outputOrdinal != 1 || sorted.keys[0].targetIdentity != 1 {
				t.Fatal("ORDER duplicate label changed analyzed target occurrence", sorted.keys)
			}
		}
	}
}

func TestQueryOrderDuplicateSubqueryLabelFirstOccurrence(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT (SELECT max(v) FROM t) AS x,(SELECT max(t.v) FROM t) AS x FROM t ORDER BY x")
	defer source.close()
	project := query.Root.(*projectNode)
	sorted := project.child.(*sortNode)
	first := project.expressions[0].(cachedOrderExpression)
	second := project.expressions[1].(cachedOrderExpression)
	if sorted.keys[0].outputOrdinal != 1 || sorted.keys[0].cache != first.slot || sorted.keys[0].cache == second.slot {
		t.Fatal("ordinary ORDER duplicate label changed first output/cache occurrence", sorted.keys, first, second)
	}
	if len(project.Schema()) != 2 || project.Schema()[0].Name != "x" || project.Schema()[1].Name != "x" {
		t.Fatal("ordinary ORDER changed visible duplicate output metadata", project.Schema())
	}
}
