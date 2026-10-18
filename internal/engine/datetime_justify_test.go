package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDatetimeJustifyBindingPropertiesRemap(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	var previous BoundExpr
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		node, err := b.bindDatetimeJustify(datetimeMakeRaw(name, ageFiniteTyped("interval", "30 days")), true)
		if err != nil {
			t.Fatal(err)
		}
		e := node.(functions.DatetimeJustify[BoundExpr, ExprContext, ExprProperties])
		if e.ResultUsage().Type.ID() != sqltype.Interval || e.ResultUsage().Typmod != -1 || e.Properties().Volatility != Immutable || plannedVolatility(e) != Immutable || !generationImmutable(e) {
			t.Fatal(e.Properties(), e.ResultUsage())
		}
		if !b.sameGroupExpression(e, e) || previous != nil && b.sameGroupExpression(previous, e) {
			t.Fatal("function identity lost")
		}
		previous = e
		var walked []BoundExpr
		boundExpressionChildren(e, func(child BoundExpr) { walked = append(walked, child) })
		if len(walked) != 1 || walked[0] != e.Arg {
			t.Fatal("unary traversal", walked)
		}
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		next, changed, err := retagBoundTypes(New().CatalogRead(), e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal(next, changed, err)
		}
		rebound := next.(functions.DatetimeJustify[BoundExpr, ExprContext, ExprProperties])
		if rebound.ResultUsage().Type != replacements[sqltype.Interval] || rebound.Arg.ResultUsage().Type != replacements[sqltype.Interval] {
			t.Fatal("foreign retained authority")
		}
		value, err := rebound.Eval(&EvalContext{Context: context.Background()})
		wantMonths, wantDays := int32(1), int32(0)
		if name == "justify_hours" {
			wantMonths, wantDays = 0, 30
		}
		months, days, clock, failure := value.Interval()
		if err != nil || failure != nil || months != wantMonths || days != wantDays || clock != 0 || value.Type() != replacements[sqltype.Interval] {
			t.Fatal(name, value, err, failure)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := rebound.Eval(&EvalContext{Context: ctx}); !errors.Is(err, context.Canceled) {
			t.Fatal(name, err)
		}
	}
}

func TestDatetimeJustifyParameterAndStrictNull(t *testing.T) {
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		node, err := b.bindDatetimeJustify(datetimeMakeRaw(name, parser.ParameterExpr{Number: 1}), true)
		if err != nil {
			t.Fatal(err)
		}
		if b.parameters[1].Type.ID() != sqltype.Interval || !node.Properties().ParameterDependent {
			t.Fatal(node.Properties(), b.parameters)
		}
		null, err := b.bindDatetimeJustify(datetimeMakeRaw(name, ageFiniteTyped("interval", "0")), true)
		if err != nil {
			t.Fatal(err)
		}
		e := null.(functions.DatetimeJustify[BoundExpr, ExprContext, ExprProperties])
		e.Arg = literalExpression{boundExpressionInfo(e), sqltype.Null(e.ResultUsage().Type)}
		v, err := e.Eval(&EvalContext{Context: context.Background()})
		if err != nil || !v.IsNull() || v.Type() != e.ResultUsage().Type {
			t.Fatal(v, err)
		}
		simplified, changed, err := simplifyConditional(&EvalContext{Context: context.Background()}, e, false)
		if err != nil || !changed {
			t.Fatal(simplified, changed, err)
		}
	}
}
