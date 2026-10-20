package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestLikeEvaluationAndProperties(t *testing.T) {
	var text, boolean *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
		if d.ID() == sqltype.Bool {
			boolean = d
		}
	}
	provider, err := sqltype.BindLike("~~", text.Usage(), text.Usage(), boolean.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("demanded pattern child")
	left := mathObservedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	right := mathObservedArgument{Info: expr.Info{Usage: text.Usage()}, order: &order, index: 1, err: failure}
	node := plan.LikeExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: left, Right: right, Provider: provider, Operator: "~~"}
	if _, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	p := likeProperties(plan.ColumnExpr{Info: expr.Info{Property: expr.Properties{RowDependent: true}}}, plan.ParameterExpr{Info: expr.Info{Property: expr.Properties{ParameterDependent: true, StageDependent: true, Volatility: expr.Stable}}})
	if p != (expr.Properties{RowDependent: true, ParameterDependent: true, StageDependent: true, Volatility: expr.Stable}) {
		t.Fatal(p)
	}
}
