package engine

import (
	"context"
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
	b := newExpressionBinder(ctx, e, sqltype.InputEnvironment{}, nil, nil)
	literal, err := literalSQL(ctx, literalExpression{b.info(u, parser.Position{}, ExprProperties{}), value})
	if err != nil || literal != "INTERVAL '+12 months +0 days +00:00:00' YEAR" {
		t.Fatal(literal, err)
	}
	statements, err := parser.Parse(`CREATE TABLE iv(v "interval"(4,2) DEFAULT ` + literal + ")")
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*parser.CreateTable).Elements[0].Column.TypeName
	recovered, err := e.resolveType(raw, nil)
	if err != nil || recovered.Typmod != 262146 {
		t.Fatal(recovered, err)
	}
}
