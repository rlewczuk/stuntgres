package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
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
	literal := func(s string) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, s)}
	}
	ep, err := sqltype.BindSimilarEscape(text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	normalizer := functions.SimilarEscape{Info: expr.Info{Usage: text.Usage()}, Pattern: literal("a"), Escape: literal("ab"), Provider: ep}
	mp, err := sqltype.BindSimilar("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	null := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}
	node := functions.Similar{Info: expr.Info{Usage: boolean.Usage()}, Left: null, Right: normalizer, Provider: mp, Operator: "~"}
	_, _, err = preflightCoercions(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, node)
	state(t, err, "22025")
	normalizer.Pattern = null
	node.Right = normalizer
	folded, _, err := preflightCoercions(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, node)
	if err != nil {
		t.Fatal(err)
	}
	value, err := folded.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
	if err != nil || !value.IsNull() {
		t.Fatal(value, err)
	}
}

func TestSimilarDependencyChildren(t *testing.T) {
	text, boolean := similarUnitTypes()
	source := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage()}, Slot: 0}
	pattern := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage()}, Slot: 1}
	escape := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage()}, Slot: 2}
	helper := functions.SimilarEscape{Info: expr.Info{Usage: text.Usage()}, Pattern: pattern, Escape: escape}
	predicate := functions.Similar{Info: expr.Info{Usage: boolean.Usage()}, Left: source, Right: helper}
	extraction := functions.SimilarSubstring{Info: expr.Info{Usage: text.Usage()}, Args: [3]expr.Expr{source, pattern, escape}}
	for _, node := range []expr.Expr{predicate, extraction} {
		var slots []int
		plan.WalkExpression(node, func(n expr.Expr) {
			if column, ok := n.(plan.ColumnExpr); ok {
				slots = append(slots, column.Slot)
			}
		})
		if !reflect.DeepEqual(slots, []int{0, 1, 2}) {
			t.Fatal("lost source/pattern/escape dependency", slots)
		}
	}
}
