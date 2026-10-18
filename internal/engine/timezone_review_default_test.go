package engine

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestTimezoneReviewStoredStableDefaultValidation(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, expression := range []string{
		"TIMESTAMP '2004-07-01 12:00' AT LOCAL",
		"TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'America/New_York'",
		"DATE '2004-10-19'::TIMESTAMPTZ",
		"DATE '2004-10-19'::TEXT",
		"TIMESTAMP '2004-10-19 12:00'::TEXT",
		"DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00'",
		"CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END",
		"NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00')",
		"TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'No/Such_Zone'",
	} {
		t.Run(expression, func(t *testing.T) {
			binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			node, err := binder.bind(castTarget(t, "SELECT "+expression+" FROM t"), expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if got := node.Properties(); got != (ExprProperties{Volatility: Stable}) {
				t.Fatalf("properties = %+v", got)
			}
			if text, err := defaultSQL(ctx, e.CatalogRead(), node); err != nil || text == "" {
				t.Fatalf("serialize closed Stable definition without Eval: %q %v", text, err)
			}
		})
	}
}

func TestTimezoneReviewDefaultDependencyRejection(t *testing.T) {
	e := New()
	ctx := context.Background()
	for _, properties := range []ExprProperties{
		{Volatility: Volatile}, {Volatility: Volatility(255)},
		{Volatility: Stable, RowDependent: true},
		{Volatility: Stable, ParameterDependent: true},
		{Volatility: Stable, StageDependent: true},
	} {
		binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		node, err := binder.bind(castTarget(t, "SELECT TRUE FROM t"), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		literal := node.(literalExpression)
		literal.expressionInfo.Property = properties
		if _, err := defaultSQL(ctx, e.CatalogRead(), literal); err == nil {
			t.Fatalf("accepted forbidden properties %+v", properties)
		}
	}
	for _, expression := range []string{"id", "$1", "random()", "nextval('s')"} {
		binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		typ, ok := e.CatalogRead().LookupType(sqltype.Int4)
		if !ok {
			t.Fatal("missing int4")
		}
		column := ColumnDescriptor{Name: "v", Usage: typ.Usage()}
		if _, err := binder.bindDefault(castTarget(t, "SELECT "+expression+" FROM t"), column); err == nil {
			t.Fatalf("accepted unsupported default %s", expression)
		}
	}
}
