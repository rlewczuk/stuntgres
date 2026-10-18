package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryDistinctOnSubquerySemanticIdentity(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"(SELECT max(v) FROM t WHERE id>1)", "(SELECT /* λ */ max(v) FROM t WHERE id>1)", true},
		{"(SELECT v FROM t)", "(SELECT t.v FROM t)", true},
		{"(SELECT q.v FROM t AS q)", "(SELECT r.v FROM t AS r)", false},
		{"(SELECT max(v) FROM t WHERE id>1)", "(SELECT max(v) FROM t WHERE id>2)", false},
		{"(SELECT max(v) FROM t WHERE id>1)", "(SELECT max(v) FROM t WHERE id<1)", false},
		{"(SELECT q.v FROM t AS q)", "(SELECT t.v FROM t AS q)", false},
		{"(SELECT v FROM t AS q WHERE id=t.id)", "(SELECT q.v FROM t AS q WHERE q.id=t.id)", true},
		{"(SELECT q.v FROM t AS q WHERE q.id=t.id)", "(SELECT q.v FROM t AS q WHERE q.id=t.v)", false},
		{"(SELECT v FROM t)", "(SELECT v FROM (SELECT id,v FROM t) AS q)", false},
		{"(SELECT v AS x FROM t AS q(id,v,f))", "(SELECT other AS x FROM t AS q(id,other,f))", false},
		{"(SELECT t.v FROM t)", "(SELECT t.v FROM t AS t)", false},
	} {
		_, query, source := queryCoreFixture(t, "SELECT "+tc.left+","+tc.right+" FROM t")
		project := query.Root.(*projectNode)
		a, b := project.expressions[0], project.expressions[1]
		got := (*expressionBinder)(nil).sameSubqueryTarget(a, b)
		source.close()
		if got != tc.same {
			t.Fatal(tc.left, tc.right, got, tc.same)
		}
		if got := (*expressionBinder)(nil).sameQueryTarget(a, b); got != tc.same {
			t.Fatal("binding-local target predicate", tc.left, tc.right, got, tc.same)
		}
		if sameGroupExpression(a, b) {
			t.Fatal("ordinary comparison acquired local subquery sharing")
		}
	}
}

func TestQueryDistinctOnSameSubqueryTargetAndRemap(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON((SELECT max(v) FROM t)) id FROM t ORDER BY (SELECT /* λ */ max(t.v) FROM t),id DESC")
	defer source.close()
	node := query.Root.(*projectNode).child.(*distinctOnNode)
	sorted := node.child.(*sortNode)
	key := node.expressions[0].(cachedOrderExpression)
	if key.slot != sorted.keys[0].cache || len(sorted.outputs) != 0 {
		t.Fatal("ON/ORDER same-query datum not shared", key, sorted.keys, sorted.outputs)
	}
	fresh := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		fresh[d.ID()] = d
	}
	remapped, err := remapViewQuery(New().CatalogRead(), query.Root, func(r ids.ObjectRef) ids.ObjectRef { return r }, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	next := remapped.(*projectNode).child.(*distinctOnNode)
	sort := next.child.(*sortNode)
	if next.expressions[0].(cachedOrderExpression).slot != sort.keys[0].cache || next.expressions[0].ResultUsage().Type != fresh[sqltype.Int4] {
		t.Fatal("remapped scoped target sharing lost", next, sort)
	}
}

func TestQueryDistinctOnSubqueryFrameAndSourceGuards(t *testing.T) {
	var frames [4][2]uint32
	c := queryTargetComparison{frames: frames[:0]}
	if !c.pairFrame(2, 3) || !c.sameFrame(2, 3) || c.sameFrame(2, 2) || c.sameFrame(1, 3) || !c.sameFrame(1, 1) || c.pairFrame(2, 4) || c.pairFrame(4, 3) {
		t.Fatal("non-bijective child/outer frame identity", c)
	}
	_, query, source := queryCoreFixture(t, "SELECT (SELECT v FROM t) FROM t")
	defer source.close()
	root := query.Root.(*projectNode).expressions[0].(subqueryExpression).queryPlan.Root.(*projectNode)
	leaf := root.child.(*sourceNode)
	changed := *leaf
	changed.ref = ids.ObjectRef{}
	var occurrences [4]queryTargetOccurrence
	c = queryTargetComparison{frames: frames[:0], occurrences: occurrences[:0]}
	if c.sameQuery(nil, leaf, &changed) {
		t.Fatal("distinct physical source references shared")
	}
	c.leftFrame, c.rightFrame = 2, 3
	if !c.pairOccurrence(1, 4) || !c.pairOccurrence(1, 4) || c.pairOccurrence(1, 5) || c.pairOccurrence(2, 4) {
		t.Fatal("non-bijective physical occurrence identity", c)
	}
}
