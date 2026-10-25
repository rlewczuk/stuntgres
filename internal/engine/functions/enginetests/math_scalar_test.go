package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMathScalarBinding(t *testing.T) {
	for _, tc := range []struct {
		query      string
		result     sqltype.ID
		parameters []sqltype.ID
	}{
		{"SELECT abs(-17)", sqltype.Int4, nil}, {"SELECT abs(9223372036854775807)", sqltype.Int8, nil}, {"SELECT abs(-17.400)", sqltype.Numeric, nil},
		{"SELECT abs(NULL)", sqltype.Float8, nil}, {"SELECT abs($1)", sqltype.Float8, []sqltype.ID{sqltype.Float8}},
		{"SELECT ceil(42)", sqltype.Float8, nil}, {"SELECT ceiling(-42.8)", sqltype.Numeric, nil}, {"SELECT floor(NULL)", sqltype.Float8, nil},
		{"SELECT exp($1)", sqltype.Float8, []sqltype.ID{sqltype.Float8}}, {"SELECT exp(1.0)", sqltype.Numeric, nil},
		{"SELECT div($1,$2)", sqltype.Numeric, []sqltype.ID{sqltype.Numeric, sqltype.Numeric}}, {"SELECT factorial($1)", sqltype.Numeric, []sqltype.ID{sqltype.Int8}},
		{"SELECT pg_catalog.abs(floor(-42.8))", sqltype.Numeric, nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
			raw := enginetest.Statement(t, tc.query).(*parser.Select).Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if node.ResultUsage().Type.ID() != tc.result || node.ResultUsage().Typmod != -1 {
				t.Fatal(node.ResultUsage())
			}
			for i, id := range tc.parameters {
				if b.Params.Parameters[i+1].Type == nil || b.Params.Parameters[i+1].Type.ID() != id {
					t.Fatal("parameter contract", b.Params.Parameters)
				}
			}
		})
	}
}
