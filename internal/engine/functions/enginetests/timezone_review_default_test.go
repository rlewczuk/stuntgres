package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTimezoneReviewStoredStableDefaultValidation(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
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
			binder := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			node, err := binder.Bind(castTarget(t, "SELECT "+expression+" FROM t"), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if got := node.Properties(); got != (expr.Properties{Volatility: expr.Stable}) {
				t.Fatalf("properties = %+v", got)
			}
			if text, err := stored.DefaultSQL(ctx, e.CatalogRead(), node); err != nil || text == "" {
				t.Fatalf("serialize closed Stable definition without Eval: %q %v", text, err)
			}
		})
	}
}

func TestTimezoneReviewDefaultDependencyRejection(t *testing.T) {
	e := engine.New()
	ctx := context.Background()
	for _, properties := range []expr.Properties{
		{Volatility: expr.Volatile}, {Volatility: expr.Volatility(255)},
		{Volatility: expr.Stable, RowDependent: true},
		{Volatility: expr.Stable, ParameterDependent: true},
		{Volatility: expr.Stable, StageDependent: true},
	} {
		binder := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		node, err := binder.Bind(castTarget(t, "SELECT TRUE FROM t"), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		literal := node.(plan.LiteralExpr)
		literal.Info.Property = properties
		if _, err := stored.DefaultSQL(ctx, e.CatalogRead(), literal); err == nil {
			t.Fatalf("accepted forbidden properties %+v", properties)
		}
	}
	for _, expression := range []string{"id", "$1", "random()", "nextval('s')"} {
		binder := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		typ, ok := e.CatalogRead().LookupType(sqltype.Int4)
		if !ok {
			t.Fatal("missing int4")
		}
		column := catalog.Column{Name: "v", Usage: typ.Usage()}
		if _, err := binder.BindDefault(castTarget(t, "SELECT "+expression+" FROM t"), column); err == nil {
			t.Fatalf("accepted unsupported default %s", expression)
		}
	}
}

func castTarget(t *testing.T, q string) parser.Expr {
	t.Helper()
	return enginetest.Statement(t, q).(*parser.Select).Targets[0]
}
