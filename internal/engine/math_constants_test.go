package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMathConstantsPiConsumers(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	bind := func(text string) expr.Expr {
		t.Helper()
		raw := statement(t, "SELECT "+text).(*parser.Select).Targets[0]
		x, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	node := bind("pi()")
	original, ok := node.(functions.Math)
	if !ok || len(original.Args) != 0 || original.Plan.Name() != "pi" || plannedVolatility(node) != expr.Immutable {
		t.Fatal(node)
	}
	if !b.sameGroupExpression(node, bind("pg_catalog.pi()")) {
		t.Fatal("zero-child grouping identity")
	}
	visits := 0
	walkBoundExpression(node, func(expr.Expr) { visits++ })
	if visits != 1 {
		t.Fatal("zero-child traversal", visits)
	}
	rewritten, err := b.rewriteGroup(node, nil)
	if err != nil || !b.sameGroupExpression(node, rewritten) {
		t.Fatal("group rewrite", rewritten, err)
	}
	assert := func(x expr.Expr, owner *sqltype.Descriptor) {
		t.Helper()
		value, err := x.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil || value.IsNull() || value.Type() != owner {
			t.Fatal(value, err)
		}
		text, err := owner.Output(ctx, value)
		if err != nil || text != "3.141592653589793" {
			t.Fatal(text, err)
		}
	}
	assert(node, original.Usage.Type)
	next, changed, err := retagBoundTypes(target.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		d, _ := target.CatalogRead().LookupType(old.ID())
		return d
	})
	if err != nil || !changed || next.ResultUsage().Type == original.Usage.Type {
		t.Fatal("pi result owner retag", next, changed, err)
	}
	reminted := next.(functions.Math)
	if reminted.Plan.ResultUsage() != reminted.Usage || original.Plan.ResultUsage() != original.Usage {
		t.Fatal("retag mutated or retained old provider")
	}
	assert(next, reminted.Usage.Type)
	assert(node, original.Usage.Type)
	rendered, err := defaultSQL(ctx, target.CatalogRead(), next)
	if err != nil || rendered != "pg_catalog.pi()" {
		t.Fatal("stored zero-child rendering", rendered, err)
	}
	foreign := reminted
	foreign.Plan = original.Plan
	if _, err := defaultSQL(ctx, target.CatalogRead(), foreign); err == nil {
		t.Fatal("stored foreign provider accepted")
	}
	foreign = reminted
	foreign.Args = []expr.Expr{literalExpression{Info: expr.Info{Usage: reminted.Usage}, value: sqltype.Null(reminted.Usage.Type)}}
	if _, err := defaultSQL(ctx, target.CatalogRead(), foreign); err == nil {
		t.Fatal("stored pi argument accepted")
	}
	invalidPlan, err := sqltype.BindMath("cbrt", reminted.Usage, reminted.Usage)
	if err != nil {
		t.Fatal(err)
	}
	foreign = reminted
	foreign.Plan = invalidPlan
	if _, err := defaultSQL(ctx, target.CatalogRead(), foreign); err == nil {
		t.Fatal("stored unary with no child accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	value, err := next.Eval(&EvalContext{evalStatement: evalStatement{Context: canceled}})
	if !errors.Is(err, context.Canceled) || value.Type() != nil {
		t.Fatal("pi cancellation publication", value, err)
	}
	b.consumer.defaultConsumer = true
	raw := statement(t, "SELECT pi()").(*parser.Select).Targets[0]
	if _, err := b.bind(raw, expressionPolicy{context: projectionContext}); err == nil {
		t.Fatal("widened default admission")
	}
}
