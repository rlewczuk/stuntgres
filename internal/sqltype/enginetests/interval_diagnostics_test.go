package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIntervalLiteralOverflowPosition(t *testing.T) {
	e := engine.New()
	d, _ := e.CatalogRead().LookupType(sqltype.Interval)
	b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "2147483648 days", Pos: parser.Position{Offset: 22, Character: 23}}}
	_, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.AssignmentContext, Expected: d.Usage(), Mode: sqltype.Assignment})
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22015", Message: "interval field value out of range: \"2147483648 days\"", Position: "23"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, want)
	}
}
