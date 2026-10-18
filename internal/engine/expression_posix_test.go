package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPosixDependencyAndPresentationIdentity(t *testing.T) {
	text, boolean := similarUnitTypes()
	source := columnExpression{Info: expr.Info{Usage: text.Usage()}, slot: 0}
	pattern := columnExpression{Info: expr.Info{Usage: text.Usage()}, slot: 1}
	predicate := functions.Posix{Info: expr.Info{Usage: boolean.Usage()}, Left: source, Right: pattern, Operator: "~"}
	extraction := functions.PosixSubstring{Info: expr.Info{Usage: text.Usage()}, Args: [2]expr.Expr{source, pattern}}
	for _, node := range []expr.Expr{predicate, extraction} {
		var slots []int
		walkBoundExpression(node, func(n expr.Expr) {
			if c, ok := n.(columnExpression); ok {
				slots = append(slots, c.slot)
			}
		})
		if !reflect.DeepEqual(slots, []int{0, 1}) {
			t.Fatal("lost regex dependency", slots)
		}
	}
	standard := extraction
	standard.Syntax = parser.SQLFunctionSyntax
	if !sameGroupExpression(extraction, standard) || !sameGroupExpression(standard, extraction) {
		t.Fatal("regex substring presentation changed grouping identity")
	}
	swapped := extraction
	swapped.Args = [2]expr.Expr{pattern, source}
	if sameGroupExpression(extraction, swapped) {
		t.Fatal("regex operands lost grouping identity")
	}
	otherOperator := predicate
	otherOperator.Operator = "~*"
	if sameGroupExpression(predicate, otherOperator) {
		t.Fatal("regex operator lost grouping identity")
	}
}

func TestPosixCommonStatementOwner(t *testing.T) {
	text, boolean := similarUnitTypes()
	info := expr.Info{Usage: text.Usage()}
	literal := func(s string) expr.Expr {
		return literalExpression{Info: info, value: sqltype.String(text, s)}
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
	p := functions.Posix{Info: expr.Info{Usage: boolean.Usage()}, Left: literal("AbC"), Right: literal("abc"), Provider: pp, Operator: "~*"}
	s := functions.Similar{Info: expr.Info{Usage: boolean.Usage()}, Left: literal("AbC"), Right: literal("abc"), Provider: sp, Operator: "~"}
	for _, node := range []expr.Expr{p, s, p} {
		value, err := node.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		expected := true
		if _, ok := node.(functions.Similar); ok {
			expected = false
		}
		got, err := value.Boolean()
		if err != nil || got != expected {
			t.Fatal(got, err, expected)
		}
	}
}
