package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntervalLiteralOverflowPosition(t *testing.T) {
	e := New()
	d, _ := e.CatalogRead().LookupType(sqltype.Interval)
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "2147483648 days", Pos: parser.Position{Offset: 22, Character: 23}}}
	_, err := b.Bind(raw, expressionPolicy{Context: AssignmentContext, Expected: d.Usage(), Mode: sqltype.Assignment})
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22015", Message: "interval field value out of range: \"2147483648 days\"", Position: "23"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, want)
	}
}
