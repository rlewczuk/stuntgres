package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDatetimeFormattingViewCastUsage(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	timestamp, text := b.Type(sqltype.Timestamp), b.Type(sqltype.Text)
	value, err := sqltype.TimestampValue(timestamp, 0)
	if err != nil {
		t.Fatal(err)
	}
	precision := int32(3)
	target := timestamp.Usage()
	target.Typmod = precision
	coercionPlan, err := sqltype.PlanCoercion(timestamp.Usage(), target, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	raw := parser.CastExpr{
		Child:    parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "timestamp"}, Text: "2000-01-01 00:00:00"},
		TypeName: parser.TypeName{Name: "timestamp", Modifiers: []parser.TypeModifier{{Integer: &precision}}},
	}
	node := plan.CoercionExpr{Info: expr.Info{Usage: target}, Child: plan.LiteralExpr{Info: expr.Info{Usage: timestamp.Usage()}, Value: value}, Plan: coercionPlan, Mode: sqltype.Explicit}
	if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	key := analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral}
	renderer := stored.QueryRenderer{View: stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}), Frame: b.FrameID()}
	catalog := e.CatalogRead()
	for _, c := range []struct {
		deparse, pretty bool
		want            string
	}{
		{false, false, "CAST(TIMESTAMP '2000-01-01 00:00:00' AS TIMESTAMP(3))"},
		{true, true, "('2000-01-01 00:00:00'::timestamp without time zone)::timestamp(3) without time zone"},
		{true, false, "('2000-01-01 00:00:00'::timestamp without time zone)::timestamp(3) without time zone"},
	} {
		renderer.Presentation = stored.Presentation{Deparse: c.deparse, Pretty: c.pretty}
		got, err := renderer.ExpressionSQL(ctx, catalog, raw, key)
		if err != nil || got != c.want {
			t.Fatal("cast replay/presentation", c, got, err)
		}
	}
	outerPlan, err := sqltype.PlanCoercion(target, text.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	outerRaw := parser.CastExpr{Child: raw, TypeName: parser.TypeName{Name: "text"}}
	outerNode := plan.CoercionExpr{Info: expr.Info{Usage: text.Usage()}, Child: node, Plan: outerPlan, Mode: sqltype.Explicit}
	b.Capture.Literals, b.Capture.CastUsages = nil, nil
	if err := b.CaptureViewLiterals(outerRaw, outerNode, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer = stored.QueryRenderer{View: stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}), Frame: b.FrameID(), Presentation: stored.Presentation{Deparse: true, Pretty: true}}
	got, err := renderer.ExpressionSQL(ctx, catalog, outerRaw, key)
	want := "(('2000-01-01 00:00:00'::timestamp without time zone)::timestamp(3) without time zone)::text"
	if err != nil || got != want {
		t.Fatal("nested cast grouping", got, want, err)
	}
}

func TestDatetimeFormattingViewOIDCastReplay(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	raw := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "42"}}, TypeName: parser.TypeName{Name: "oid"}}
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := stored.QueryRenderer{View: stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}), Frame: b.FrameID()}
	for _, c := range []struct {
		deparse bool
		want    string
	}{
		{false, "CAST('42' AS OID)"}, {true, "'42'::oid"},
	} {
		renderer.Presentation = stored.Presentation{Deparse: c.deparse, Pretty: true}
		got, err := renderer.ExpressionSQL(ctx, e.CatalogRead(), raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
		if err != nil || got != c.want {
			t.Fatal("OID literal acquired duplicate cast", c, got, err)
		}
	}
}
