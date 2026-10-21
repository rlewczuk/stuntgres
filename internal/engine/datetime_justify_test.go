package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDatetimeJustifyBindingPropertiesRemap(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	var previous expr.Expr
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		node, err := b.BindFunctionProvider(datetimeMakeRaw(name, ageFiniteTyped("interval", "30 days")))
		if err != nil {
			t.Fatal(err)
		}
		e := node.(functions.DatetimeJustify)
		if e.ResultUsage().Type.ID() != sqltype.Interval || e.ResultUsage().Typmod != -1 || e.Properties().Volatility != expr.Immutable || exec.PlannedVolatility(e) != expr.Immutable || !plan.GenerationImmutable(e) {
			t.Fatal(e.Properties(), e.ResultUsage())
		}
		if !b.SameGroupExpression(e, e) || previous != nil && b.SameGroupExpression(previous, e) {
			t.Fatal("function identity lost")
		}
		previous = e
		var walked []expr.Expr
		plan.ExpressionChildren(e, func(child expr.Expr) { walked = append(walked, child) })
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
		rebound := next.(functions.DatetimeJustify)
		if rebound.ResultUsage().Type != replacements[sqltype.Interval] || rebound.Arg.ResultUsage().Type != replacements[sqltype.Interval] {
			t.Fatal("foreign retained authority")
		}
		value, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
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
		if _, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) {
			t.Fatal(name, err)
		}
	}
}

func TestDatetimeJustifyParameterAndStrictNull(t *testing.T) {
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		node, err := b.BindFunctionProvider(datetimeMakeRaw(name, parser.ParameterExpr{Number: 1}))
		if err != nil {
			t.Fatal(err)
		}
		if b.Params.Parameters[1].Type.ID() != sqltype.Interval || !node.Properties().ParameterDependent {
			t.Fatal(node.Properties(), b.Params.Parameters)
		}
		null, err := b.BindFunctionProvider(datetimeMakeRaw(name, ageFiniteTyped("interval", "0")))
		if err != nil {
			t.Fatal(err)
		}
		e := null.(functions.DatetimeJustify)
		e.Arg = plan.LiteralExpr{Info: expr.InfoOf(e), Value: sqltype.Null(e.ResultUsage().Type)}
		v, err := e.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil || !v.IsNull() || v.Type() != e.ResultUsage().Type {
			t.Fatal(v, err)
		}
		simplified, changed, err := exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e, false)
		if err != nil || !changed {
			t.Fatal(simplified, changed, err)
		}
	}
}
