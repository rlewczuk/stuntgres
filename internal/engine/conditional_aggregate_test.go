package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestConditionalAggregateGroupingIdentityAndRewrite(t *testing.T) {
	b := expressionBinder{ctx: context.Background()}
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			integer = typ
		}
	}
	info := expressionInfo{Usage: integer.Usage()}
	key := columnExpression{Info: info, slot: 0}
	key.Property.RowDependent = true
	count := aggregateExpression{Info: info, slot: 0}
	count.Property.StageDependent = true
	plan, err := sqltype.PlanCoercion(integer.Usage(), integer.Usage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	equality, err := sqltype.BindComparison(integer, integer, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	arm := caseBranch{condition: key, result: key, testPlan: plan, equality: equality, testUsage: integer.Usage()}
	groupCase := caseExpression{Info: info, test: key, arms: []caseBranch{arm}, otherwise: key}
	groupList := conditionalExpression{Info: info, kind: parser.Coalesce, args: []BoundExpr{groupCase, key}}
	// Aggregates cannot occur in grouping keys. Use scalar-only keys for
	// structural identity, and a separate aggregate-containing tree for rewrite.
	original := groupCase
	original.arms = []caseBranch{arm}
	original.arms[0].result = count
	list := conditionalExpression{Info: info, kind: parser.Coalesce, args: []BoundExpr{original, count}}
	if !b.sameGroupExpression(groupCase, groupCase) || !b.sameGroupExpression(groupList, groupList) {
		t.Fatal("identical conditional grouping keys differ")
	}
	changed := groupCase
	changed.arms = []caseBranch{arm}
	otherKey := key
	otherKey.slot = 1
	changed.arms[0].condition = otherKey
	if b.sameGroupExpression(groupCase, changed) {
		t.Fatal("different WHEN expression accepted as grouping key")
	}
	changedList := groupList
	changedList.kind = parser.Greatest
	if b.sameGroupExpression(groupList, changedList) {
		t.Fatal("different conditional kind accepted as grouping key")
	}
	if aggregateOccurrence(list) == nil {
		t.Fatal("nested aggregate occurrence lost")
	}
	rewritten, err := b.rewriteGroup(list, []BoundExpr{key})
	if err != nil {
		t.Fatal(err)
	}
	out := rewritten.(conditionalExpression)
	branch := out.args[0].(caseExpression)
	if _, ok := branch.test.(groupExpression); !ok {
		t.Fatal("CASE test not rewritten")
	}
	if _, ok := branch.arms[0].condition.(groupExpression); !ok {
		t.Fatal("WHEN not rewritten")
	}
	if g, ok := branch.arms[0].result.(groupExpression); !ok || g.slot != 1 {
		t.Fatal("aggregate result not rewritten")
	}
	if _, ok := branch.otherwise.(groupExpression); !ok {
		t.Fatal("ELSE not rewritten")
	}
	if !out.Properties().StageDependent || !branch.Properties().StageDependent {
		t.Fatal("stage dependence lost")
	}
	if _, ok := original.arms[0].result.(aggregateExpression); !ok {
		t.Fatal("immutable CASE arms mutated")
	}
	if _, ok := list.args[1].(aggregateExpression); !ok {
		t.Fatal("immutable list args mutated")
	}
	for _, bound := range []BoundExpr{groupCase, groupList} {
		got, err := b.rewriteGroup(bound, []BoundExpr{bound})
		if _, ok := got.(groupExpression); err != nil || !ok {
			t.Fatal("whole conditional grouping key not recognized", err)
		}
	}
}
