package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

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
	left := mathObservedArgument{Info: expressionInfo{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	right := mathObservedArgument{Info: expressionInfo{Usage: text.Usage()}, order: &order, index: 1, err: failure}
	node := likeExpression{Info: expressionInfo{Usage: boolean.Usage()}, left: left, right: right, provider: provider, operator: "~~"}
	if _, err := node.Eval(&EvalContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	p := likeProperties(columnExpression{Info: expressionInfo{Property: ExprProperties{RowDependent: true}}}, parameterExpression{Info: expressionInfo{Property: ExprProperties{ParameterDependent: true, StageDependent: true, Volatility: Stable}}})
	if p != (ExprProperties{RowDependent: true, ParameterDependent: true, StageDependent: true, Volatility: Stable}) {
		t.Fatal(p)
	}
}
