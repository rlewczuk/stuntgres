package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/functions"
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

func TestSimilarNormalizerPreflightDemand(t *testing.T) {
	text, boolean := similarUnitTypes()
	literal := func(s string) BoundExpr {
		return literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.String(text, s)}
	}
	ep, err := sqltype.BindSimilarEscape(text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	normalizer := functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: text.Usage()}), Pattern: literal("a"), Escape: literal("ab"), Provider: ep}
	mp, err := sqltype.BindSimilar("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	null := literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.Null(text)}
	node := functions.Similar[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: boolean.Usage()}), Left: null, Right: normalizer, Provider: mp, Operator: "~"}
	_, _, err = preflightCoercions(&EvalContext{Context: context.Background()}, node)
	state(t, err, "22025")
	normalizer.Pattern = null
	node.Right = normalizer
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
	helper := functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: text.Usage()}), Pattern: pattern, Escape: escape}
	predicate := functions.Similar[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: boolean.Usage()}), Left: source, Right: helper}
	extraction := functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: text.Usage()}), Args: [3]BoundExpr{source, pattern, escape}}
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
