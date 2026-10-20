package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
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
	info := expr.Info{Usage: integer.Usage()}
	key := columnExpression{Info: info, Slot: 0}
	key.Property.RowDependent = true
	count := aggregateExpression{Info: info, Slot: 0}
	count.Property.StageDependent = true
	coercionPlan, err := sqltype.PlanCoercion(integer.Usage(), integer.Usage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	equality, err := sqltype.BindComparison(integer, integer, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	arm := caseBranch{Condition: key, Result: key, TestPlan: coercionPlan, Equality: equality, TestUsage: integer.Usage()}
	groupCase := caseExpression{Info: info, Test: key, Arms: []caseBranch{arm}, Otherwise: key}
	groupList := conditionalExpression{Info: info, Kind: parser.Coalesce, Args: []expr.Expr{groupCase, key}}
	// Aggregates cannot occur in grouping keys. Use scalar-only keys for
	// structural identity, and a separate aggregate-containing tree for rewrite.
	original := groupCase
	original.Arms = []caseBranch{arm}
	original.Arms[0].Result = count
	list := conditionalExpression{Info: info, Kind: parser.Coalesce, Args: []expr.Expr{original, count}}
	if !b.sameGroupExpression(groupCase, groupCase) || !b.sameGroupExpression(groupList, groupList) {
		t.Fatal("identical conditional grouping keys differ")
	}
	changed := groupCase
	changed.Arms = []caseBranch{arm}
	otherKey := key
	otherKey.Slot = 1
	changed.Arms[0].Condition = otherKey
	if b.sameGroupExpression(groupCase, changed) {
		t.Fatal("different WHEN expression accepted as grouping key")
	}
	changedList := groupList
	changedList.Kind = parser.Greatest
	if b.sameGroupExpression(groupList, changedList) {
		t.Fatal("different conditional kind accepted as grouping key")
	}
	if aggregateOccurrence(list) == nil {
		t.Fatal("nested aggregate occurrence lost")
	}
	rewritten, err := b.rewriteGroup(list, []expr.Expr{key})
	if err != nil {
		t.Fatal(err)
	}
	out := rewritten.(conditionalExpression)
	branch := out.Args[0].(caseExpression)
	if _, ok := branch.Test.(groupExpression); !ok {
		t.Fatal("CASE test not rewritten")
	}
	if _, ok := branch.Arms[0].Condition.(groupExpression); !ok {
		t.Fatal("WHEN not rewritten")
	}
	if g, ok := branch.Arms[0].Result.(groupExpression); !ok || g.Slot != 1 {
		t.Fatal("aggregate result not rewritten")
	}
	if _, ok := branch.Otherwise.(groupExpression); !ok {
		t.Fatal("ELSE not rewritten")
	}
	if !out.Properties().StageDependent || !branch.Properties().StageDependent {
		t.Fatal("stage dependence lost")
	}
	if _, ok := original.Arms[0].Result.(aggregateExpression); !ok {
		t.Fatal("immutable CASE arms mutated")
	}
	if _, ok := list.Args[1].(aggregateExpression); !ok {
		t.Fatal("immutable list args mutated")
	}
	for _, bound := range []expr.Expr{groupCase, groupList} {
		got, err := b.rewriteGroup(bound, []expr.Expr{bound})
		if _, ok := got.(groupExpression); err != nil || !ok {
			t.Fatal("whole conditional grouping key not recognized", err)
		}
	}
}
