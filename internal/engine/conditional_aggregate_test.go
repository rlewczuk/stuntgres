package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestConditionalAggregateGroupingIdentityAndRewrite(t *testing.T) {
	b := analyze.Binder{Ctx: context.Background()}
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			integer = typ
		}
	}
	info := expr.Info{Usage: integer.Usage()}
	key := plan.ColumnExpr{Info: info, Slot: 0}
	key.Property.RowDependent = true
	count := plan.AggregateExpr{Info: info, Slot: 0}
	count.Property.StageDependent = true
	equality, err := sqltype.BindComparison(integer, integer, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	arm := plan.CaseBranch{Condition: key, Result: key, Test: plan.CaseTestExpr{Info: info}, Equality: equality, TestUsage: integer.Usage()}
	groupCase := plan.CaseExpr{Info: info, Test: key, Arms: []plan.CaseBranch{arm}, Otherwise: key}
	groupList := plan.ConditionalExpr{Info: info, Kind: parser.Coalesce, Args: []expr.Expr{groupCase, key}}
	// Aggregates cannot occur in grouping keys. Use scalar-only keys for
	// structural identity, and a separate aggregate-containing tree for rewrite.
	original := groupCase
	original.Arms = []plan.CaseBranch{arm}
	original.Arms[0].Result = count
	list := plan.ConditionalExpr{Info: info, Kind: parser.Coalesce, Args: []expr.Expr{original, count}}
	if !b.SameGroupExpression(groupCase, groupCase) || !b.SameGroupExpression(groupList, groupList) {
		t.Fatal("identical conditional grouping keys differ")
	}
	changed := groupCase
	changed.Arms = []plan.CaseBranch{arm}
	otherKey := key
	otherKey.Slot = 1
	changed.Arms[0].Condition = otherKey
	if b.SameGroupExpression(groupCase, changed) {
		t.Fatal("different WHEN expression accepted as grouping key")
	}
	changedList := groupList
	changedList.Kind = parser.Greatest
	if b.SameGroupExpression(groupList, changedList) {
		t.Fatal("different conditional kind accepted as grouping key")
	}
	if analyze.AggregateOccurrence(list) == nil {
		t.Fatal("nested aggregate occurrence lost")
	}
	rewritten, err := b.RewriteGroup(list, []expr.Expr{key})
	if err != nil {
		t.Fatal(err)
	}
	out := rewritten.(plan.ConditionalExpr)
	branch := out.Args[0].(plan.CaseExpr)
	if _, ok := branch.Test.(plan.GroupExpr); !ok {
		t.Fatal("CASE test not rewritten")
	}
	if _, ok := branch.Arms[0].Condition.(plan.GroupExpr); !ok {
		t.Fatal("WHEN not rewritten")
	}
	if g, ok := branch.Arms[0].Result.(plan.GroupExpr); !ok || g.Slot != 1 {
		t.Fatal("aggregate result not rewritten")
	}
	if _, ok := branch.Otherwise.(plan.GroupExpr); !ok {
		t.Fatal("ELSE not rewritten")
	}
	if !out.Properties().StageDependent || !branch.Properties().StageDependent {
		t.Fatal("stage dependence lost")
	}
	if _, ok := original.Arms[0].Result.(plan.AggregateExpr); !ok {
		t.Fatal("immutable CASE arms mutated")
	}
	if _, ok := list.Args[1].(plan.AggregateExpr); !ok {
		t.Fatal("immutable list args mutated")
	}
	for _, bound := range []expr.Expr{groupCase, groupList} {
		got, err := b.RewriteGroup(bound, []expr.Expr{bound})
		if _, ok := got.(plan.GroupExpr); err != nil || !ok {
			t.Fatal("whole conditional grouping key not recognized", err)
		}
	}
}
