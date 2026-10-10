package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func similarUnitTypes() (*sqltype.Descriptor, *sqltype.Descriptor) {
	var text, boolean *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Text:
			text = d
		case sqltype.Bool:
			boolean = d
		}
	}
	return text, boolean
}

func TestSimilarEvaluationDemand(t *testing.T) {
	text, boolean := similarUnitTypes()
	provider, err := sqltype.BindSimilar("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("demanded row pattern")
	left := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	right := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, err: failure, order: &order, index: 1}
	node := similarExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: left, right: right, provider: provider, operator: "~"}
	value, err := node.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0}) {
		t.Fatal(value, err, order)
	}
	order = nil
	left.value = sqltype.String(text, "a")
	node.left = left
	if _, err := node.Eval(&EvalContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(err, order)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	order = nil
	if _, err := node.Eval(&EvalContext{Context: canceled}); !errors.Is(err, context.Canceled) || len(order) != 0 {
		t.Fatal(err, order)
	}
}

func TestSimilarNormalizerPreflightDemand(t *testing.T) {
	text, boolean := similarUnitTypes()
	literal := func(s string) BoundExpr {
		return literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.String(text, s)}
	}
	ep, err := sqltype.BindSimilarEscape(text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	normalizer := similarEscapeExpression{expressionInfo: expressionInfo{usage: text.Usage()}, pattern: literal("a"), escape: literal("ab"), provider: ep}
	mp, err := sqltype.BindSimilar("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	null := literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.Null(text)}
	node := similarExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: null, right: normalizer, provider: mp, operator: "~"}
	_, _, err = preflightCoercions(&EvalContext{Context: context.Background()}, node)
	state(t, err, "22025")
	normalizer.pattern = null
	node.right = normalizer
	folded, _, err := preflightCoercions(&EvalContext{Context: context.Background()}, node)
	if err != nil {
		t.Fatal(err)
	}
	value, err := folded.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !value.IsNull() {
		t.Fatal(value, err)
	}
}

func TestSimilarDependencyChildren(t *testing.T) {
	text, boolean := similarUnitTypes()
	source := columnExpression{expressionInfo: expressionInfo{usage: text.Usage()}, slot: 0}
	pattern := columnExpression{expressionInfo: expressionInfo{usage: text.Usage()}, slot: 1}
	escape := columnExpression{expressionInfo: expressionInfo{usage: text.Usage()}, slot: 2}
	helper := similarEscapeExpression{expressionInfo: expressionInfo{usage: text.Usage()}, pattern: pattern, escape: escape}
	predicate := similarExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: source, right: helper}
	extraction := similarSubstringExpression{expressionInfo: expressionInfo{usage: text.Usage()}, args: [3]BoundExpr{source, pattern, escape}}
	for _, node := range []BoundExpr{predicate, extraction} {
		var slots []int
		walkBoundExpression(node, func(n BoundExpr) {
			if column, ok := n.(columnExpression); ok {
				slots = append(slots, column.slot)
			}
		})
		if !reflect.DeepEqual(slots, []int{0, 1, 2}) {
			t.Fatal("lost source/pattern/escape dependency", slots)
		}
	}
}
