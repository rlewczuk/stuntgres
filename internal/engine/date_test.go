package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestDateCaptureFreezeRebindAndParameters(t *testing.T) {
	ctx := context.Background()
	e := New()
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2000, 2, 29, 23, 59, 59, 0, time.UTC)}
	later := sqltype.InputEnvironment{TransactionStart: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}
	d, _ := e.CatalogRead().LookupType(sqltype.Date)
	for _, tc := range []struct{ text, want string }{{"now", "2000-02-29"}, {"today", "2000-02-29"}, {"tomorrow", "2000-03-01"}, {"yesterday", "2000-02-28"}} {
		for _, typed := range []bool{false, true} {
			pos := parser.Position{Offset: 15, Character: 16}
			var raw parser.Expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: tc.text, Pos: pos}}
			if typed {
				raw = parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "date"}, Text: tc.text, Pos: pos}
			}
			policy := expressionPolicy{context: assignmentContext, expected: d.Usage(), mode: sqltype.Assignment}
			b := newTestExpressionBinder(t, ctx, e, first, nil)
			if _, err := b.bind(raw, policy); err != nil {
				t.Fatal(err)
			}
			desc := StatementDescription{temporals: b.prepared.temporals}
			before := append([]temporalConstant(nil), desc.temporals...)
			if len(before) != 1 || before[0].typeID != sqltype.Date {
				t.Fatal(before)
			}
			if err := e.Reset(ctx); err != nil {
				t.Fatal(err)
			}
			d, _ = e.CatalogRead().LookupType(sqltype.Date)
			policy.expected = d.Usage()
			b = newTestExpressionBinder(t, ctx, e, later, &desc)
			node, err := b.bind(raw, policy)
			if err != nil {
				t.Fatal(err)
			}
			v, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.want || v.Type() != d || !reflect.DeepEqual(before, desc.temporals) {
				t.Fatal(out, err, desc.temporals)
			}
			b = newTestExpressionBinder(t, ctx, e, later, &desc)
			if _, err = b.bind(parser.ParameterExpr{Number: 1}, policy); err != nil {
				t.Fatal(err)
			}
			params, err := b.finish()
			if err != nil || params.Types[0].Type != d || len(b.prepared.temporals) != 0 {
				t.Fatal(params, err)
			}
			v, err = d.InputWithEnvironment(ctx, tc.text, -1, later)
			if err != nil {
				t.Fatal(err)
			}
			out, err = d.Output(ctx, v)
			want := map[string]string{"now": "2001-01-01", "today": "2001-01-01", "tomorrow": "2001-01-02", "yesterday": "2000-12-31"}[tc.text]
			if err != nil || out != want {
				t.Fatal(out, err)
			}
			foreign := parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "timestamp"}, Text: "now", Pos: pos}
			b = newTestExpressionBinder(t, ctx, e, later, &desc)
			if _, err = b.bind(foreign, expressionPolicy{context: projectionContext}); err == nil {
				t.Fatal("DATE capture reconstructed as TIMESTAMP")
			}
		}
	}
}
