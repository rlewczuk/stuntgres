package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMathConstantsPiConsumers(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	bind := func(text string) BoundExpr {
		t.Helper()
		raw := statement(t, "SELECT "+text).(*parser.Select).Targets[0]
		x, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	node := bind("pi()")
	original, ok := node.(mathExpression)
	if !ok || len(original.args) != 0 || original.plan.Name() != "pi" || plannedVolatility(node) != Immutable {
		t.Fatal(node)
	}
	if !b.sameGroupExpression(node, bind("pg_catalog.pi()")) {
		t.Fatal("zero-child grouping identity")
	}
	visits := 0
	walkBoundExpression(node, func(BoundExpr) { visits++ })
	if visits != 1 {
		t.Fatal("zero-child traversal", visits)
	}
	rewritten, err := b.rewriteGroup(node, nil)
	if err != nil || !b.sameGroupExpression(node, rewritten) {
		t.Fatal("group rewrite", rewritten, err)
	}
	assert := func(x BoundExpr, owner *sqltype.Descriptor) {
		t.Helper()
		value, err := x.Eval(&EvalContext{Context: ctx})
		if err != nil || value.IsNull() || value.Type() != owner {
			t.Fatal(value, err)
		}
		text, err := owner.Output(ctx, value)
		if err != nil || text != "3.141592653589793" {
			t.Fatal(text, err)
		}
	}
	assert(node, original.usage.Type)
	next, changed, err := retagBoundTypes(target.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		d, _ := target.CatalogRead().LookupType(old.ID())
		return d
	})
	if err != nil || !changed || next.ResultUsage().Type == original.usage.Type {
		t.Fatal("pi result owner retag", next, changed, err)
	}
	reminted := next.(mathExpression)
	if reminted.plan.ResultUsage() != reminted.usage || original.plan.ResultUsage() != original.usage {
		t.Fatal("retag mutated or retained old provider")
	}
	assert(next, reminted.usage.Type)
	assert(node, original.usage.Type)
	rendered, err := defaultSQL(ctx, target.CatalogRead(), next)
	if err != nil || rendered != "pg_catalog.pi()" {
		t.Fatal("stored zero-child rendering", rendered, err)
	}
	foreign := reminted
	foreign.plan = original.plan
	if _, err := defaultSQL(ctx, target.CatalogRead(), foreign); err == nil {
		t.Fatal("stored foreign provider accepted")
	}
	foreign = reminted
	foreign.args = []BoundExpr{literalExpression{expressionInfo: expressionInfo{usage: reminted.usage}, value: sqltype.Null(reminted.usage.Type)}}
	if _, err := defaultSQL(ctx, target.CatalogRead(), foreign); err == nil {
		t.Fatal("stored pi argument accepted")
	}
	invalidPlan, err := sqltype.BindMath("cbrt", reminted.usage, reminted.usage)
	if err != nil {
		t.Fatal(err)
	}
	foreign = reminted
	foreign.plan = invalidPlan
	if _, err := defaultSQL(ctx, target.CatalogRead(), foreign); err == nil {
		t.Fatal("stored unary with no child accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	value, err := next.Eval(&EvalContext{Context: canceled})
	if !errors.Is(err, context.Canceled) || value.Type() != nil {
		t.Fatal("pi cancellation publication", value, err)
	}
	b.defaultConsumer = true
	raw := statement(t, "SELECT pi()").(*parser.Select).Targets[0]
	if _, err := b.bind(raw, expressionPolicy{context: projectionContext}); err == nil {
		t.Fatal("widened default admission")
	}
}
