package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntervalGenericStoredLiteralSQL(t *testing.T) {
	ctx := context.Background()
	e := New()
	d, _ := e.CatalogRead().LookupType(sqltype.Interval)
	u, warning, err := d.ResolveModifiers(4, 2)
	if err != nil || warning != nil {
		t.Fatal(err, warning)
	}
	value, err := sqltype.IntervalValue(d, 12, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	literal, err := literalSQL(ctx, plan.LiteralExpr{Info: b.info(u, parser.Position{}, expr.Properties{}), Value: value})
	if err != nil || literal != "INTERVAL '+12 months +0 days +0 microseconds' YEAR" {
		t.Fatal(literal, err)
	}
	statements, err := parser.Parse(`CREATE TABLE iv(v "interval"(4,2) DEFAULT ` + literal + ")")
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*parser.CreateTable).Elements[0].Column.TypeName
	recovered, err := e.resolveType(raw, binderOperation(b))
	if err != nil || recovered.Typmod != 262146 {
		t.Fatal(recovered, err)
	}
}
