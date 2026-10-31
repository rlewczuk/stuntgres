package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestBitstringFormatLiteralReplay(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct{ input, canonical string }{{"b001", "001"}, {"x0f", "00001111"}, {"b", ""}} {
		raw := enginetest.FunctionCall("format", formatUnitLiteral("%s"), parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: tc.input}})
		node, err := b.Bind(raw, analyze.ExpressionPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := stored.DefaultSQL(ctx, e.CatalogRead(), node)
		want := "pg_catalog.format('%s', B'" + tc.canonical + "')"
		if err != nil || text != want {
			t.Fatal(text, want, err)
		}
	}
}

func formatUnitLiteral(text string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
}
