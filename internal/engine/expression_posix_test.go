package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPosixEvaluationDemand(t *testing.T) {
	text, boolean := similarUnitTypes()
	provider, err := sqltype.BindPosix("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := sqltype.BindPosixSubstring([2]sqltype.Usage{text.Usage(), text.Usage()}, text.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("demanded row pattern")
	left := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	right := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, err: failure, order: &order, index: 1}
	predicate := posixExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: left, right: right, provider: provider, operator: "~"}
	substring := posixSubstringExpression{expressionInfo: expressionInfo{usage: text.Usage()}, args: [2]BoundExpr{left, right}, provider: extraction}
	for _, node := range []BoundExpr{predicate, substring} {
		order = nil
		value, err := node.Eval(&EvalContext{Context: context.Background()})
		if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0}) {
			t.Fatal(value, err, order)
		}
	}
	left.value = sqltype.String(text, "a")
	predicate.left = left
	substring.args[0] = left
	for _, node := range []BoundExpr{predicate, substring} {
		order = nil
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
}

func TestPosixDependencyAndPresentationIdentity(t *testing.T) {
	text, boolean := similarUnitTypes()
	source := columnExpression{expressionInfo: expressionInfo{usage: text.Usage()}, slot: 0}
	pattern := columnExpression{expressionInfo: expressionInfo{usage: text.Usage()}, slot: 1}
	predicate := posixExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: source, right: pattern, operator: "~"}
	extraction := posixSubstringExpression{expressionInfo: expressionInfo{usage: text.Usage()}, args: [2]BoundExpr{source, pattern}}
	for _, node := range []BoundExpr{predicate, extraction} {
		var slots []int
		walkBoundExpression(node, func(n BoundExpr) {
			if c, ok := n.(columnExpression); ok {
				slots = append(slots, c.slot)
			}
		})
		if !reflect.DeepEqual(slots, []int{0, 1}) {
			t.Fatal("lost regex dependency", slots)
		}
	}
	standard := extraction
	standard.syntax = parser.SQLFunctionSyntax
	if !sameGroupExpression(extraction, standard) || !sameGroupExpression(standard, extraction) {
		t.Fatal("regex substring presentation changed grouping identity")
	}
	swapped := extraction
	swapped.args = [2]BoundExpr{pattern, source}
	if sameGroupExpression(extraction, swapped) {
		t.Fatal("regex operands lost grouping identity")
	}
	otherOperator := predicate
	otherOperator.operator = "~*"
	if sameGroupExpression(predicate, otherOperator) {
		t.Fatal("regex operator lost grouping identity")
	}
}

func TestPosixCommonStatementOwner(t *testing.T) {
	text, boolean := similarUnitTypes()
	info := expressionInfo{usage: text.Usage()}
	literal := func(s string) BoundExpr {
		return literalExpression{expressionInfo: info, value: sqltype.String(text, s)}
	}
	pp, err := sqltype.BindPosix("~*", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := sqltype.BindSimilar("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	c := &EvalContext{Context: context.Background(), pgRegex: sqltype.PGRegexExecution{}}
	p := posixExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: literal("AbC"), right: literal("abc"), provider: pp, operator: "~*"}
	s := similarExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: literal("AbC"), right: literal("abc"), provider: sp, operator: "~"}
	for _, node := range []BoundExpr{p, s, p} {
		value, err := node.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		expected := true
		if _, ok := node.(similarExpression); ok {
			expected = false
		}
		got, err := value.Boolean()
		if err != nil || got != expected {
			t.Fatal(got, err, expected)
		}
	}
}
