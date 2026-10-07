package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
)

func TestConditionalAggregateGroupingIdentityAndRewrite(t *testing.T) {
	b := expressionBinder{ctx: context.Background()}
	key := columnExpression{slot: 0}
	count := aggregateExpression{slot: 0}
	arm := caseBranch{condition: key, result: count}
	original := caseExpression{test: key, arms: []caseBranch{arm}, otherwise: key}
	list := conditionalExpression{kind: parser.Coalesce, args: []BoundExpr{original, count}}
	if !b.sameGroupExpression(original, original) || !b.sameGroupExpression(list, list) {
		t.Fatal("identical conditional grouping keys differ")
	}
	changed := original
	changed.arms = []caseBranch{{condition: count, result: count}}
	if b.sameGroupExpression(original, changed) {
		t.Fatal("different WHEN expression accepted as grouping key")
	}
	changedList := list
	changedList.kind = parser.Greatest
	if b.sameGroupExpression(list, changedList) {
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
	for _, expr := range []BoundExpr{original, list} {
		got, err := b.rewriteGroup(expr, []BoundExpr{expr})
		if _, ok := got.(groupExpression); err != nil || !ok {
			t.Fatal("whole conditional grouping key not recognized", err)
		}
	}
}
