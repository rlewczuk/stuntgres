package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDatetimeArithmeticBindingAndPhysicalVolatility(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		expression string
		result     sqltype.ID
		volatility expr.Volatility
	}{
		{"DATE '2024-01-31'+INTERVAL '1 month'", sqltype.Timestamp, expr.Immutable},
		{"DATE '2024-01-02'-DATE '2024-01-01'", sqltype.Int4, expr.Immutable},
		{"TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '0'", sqltype.Timestamptz, expr.Stable},
		{"TIMESTAMPTZ '2005-04-02 12:00-07'-INTERVAL '1 microsecond'", sqltype.Timestamptz, expr.Stable},
		{"INTERVAL '1 month'+TIMESTAMPTZ '2005-04-02 12:00-07'", sqltype.Timestamptz, expr.Stable},
		{"TIMESTAMPTZ '2005-04-02 12:00-07'-TIMESTAMPTZ '2005-04-01 12:00-07'", sqltype.Interval, expr.Immutable},
		{"TIME '12:00'=TIMETZ '12:00+00'", sqltype.Bool, expr.Stable},
		{"TIMETZ '12:00+00'=TIME '12:00'", sqltype.Bool, expr.Stable},
	} {
		e := New()
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+tc.expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		if node.ResultUsage().Type != b.typ(tc.result) || node.ResultUsage().Typmod != -1 || node.Properties().Volatility != tc.volatility || plannedVolatility(node) != tc.volatility || generationImmutable(node) != (tc.volatility == expr.Immutable) {
			t.Fatal(tc, node, node.Properties())
		}
		folded, changed, err := simplifyConditional(&EvalContext{evalStatement: evalStatement{Context: ctx}}, node, false)
		if err != nil {
			t.Fatal(err)
		}
		_, literal := folded.(literalExpression)
		if tc.volatility == expr.Stable && (changed || literal) {
			t.Fatal("stable literal arithmetic folded", tc, folded)
		}
		if tc.volatility == expr.Immutable && !literal {
			t.Fatal("immutable operation did not fold", tc, folded)
		}
	}
}

func TestDatetimeArithmeticDependenceAndVolatileDominance(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, expression := range []string{"$1::TIMESTAMPTZ+INTERVAL '0'", "INTERVAL '0'+$1::TIMESTAMPTZ"} {
		node, err := b.bind(statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil || !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Stable {
			t.Fatal(node, err)
		}
		n := node.(arithmeticExpression)
		child := parameterExpression{Info: expr.Info{Usage: b.typ(sqltype.Timestamptz).Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true, ParameterDependent: true, StageDependent: true}}, Number: 1}
		if n.Left.ResultUsage().Type.ID() == sqltype.Timestamptz {
			n.Left = child
		} else {
			n.Right = child
		}
		if plannedVolatility(n) != expr.Volatile || generationImmutable(n) {
			t.Fatal("intrinsic stable suppressed child", n)
		}
	}
}

func TestDatetimeArithmeticDefaultRegistryRebind(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	b.consumer.defaultConsumer = true
	node, err := b.bind(statement(t, "SELECT TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day' FROM t").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	// Rebinding must own the target registry result, not a source operand descriptor.
	rebound, changed, err := retagBoundTypes(target.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := target.CatalogRead().LookupType(old.ID())
		return next
	})
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	n := rebound.(arithmeticExpression)
	typ, _ := target.CatalogRead().LookupType(sqltype.Timestamptz)
	if n.ResultUsage().Type != typ || n.Provider.ResultType() != typ || !n.Provider.SessionDependent() || plannedVolatility(n) != expr.Stable {
		t.Fatal(n)
	}
	text, err := defaultSQL(ctx, target.CatalogRead(), rebound)
	if err != nil || text == "" {
		t.Fatal("symbolic target default", text, err)
	}
	original, err := defaultSQL(ctx, source.CatalogRead(), node)
	if err != nil || original != text {
		t.Fatal("rebind changed symbolic meaning", original, text, err)
	}
	if node.ResultUsage().Type == typ {
		t.Fatal("source registry ownership mutated")
	}
}
