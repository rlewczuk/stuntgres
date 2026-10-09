package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDatetimeFormattingViewCastUsage(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	timestamp, text := b.typ(sqltype.Timestamp), b.typ(sqltype.Text)
	value, err := sqltype.TimestampValue(timestamp, 0)
	if err != nil {
		t.Fatal(err)
	}
	precision := int32(3)
	target := timestamp.Usage()
	target.Typmod = precision
	plan, err := sqltype.PlanCoercion(timestamp.Usage(), target, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	raw := parser.CastExpr{
		Child:    parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "timestamp"}, Text: "2000-01-01 00:00:00"},
		TypeName: parser.TypeName{Name: "timestamp", Modifiers: []parser.TypeModifier{{Integer: &precision}}},
	}
	node := coercionExpression{expressionInfo{usage: target}, literalExpression{expressionInfo{usage: timestamp.Usage()}, value}, plan, sqltype.Explicit}
	if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	key := viewLiteralPath{clause: viewTargetLiteral}
	renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID()}
	catalog := e.CatalogRead()
	for _, c := range []struct {
		deparse, pretty bool
		want            string
	}{
		{false, false, "CAST(TIMESTAMP '2000-01-01 00:00:00' AS TIMESTAMP(3))"},
		{true, true, "('2000-01-01 00:00:00'::timestamp without time zone)::timestamp(3) without time zone"},
		{true, false, "('2000-01-01 00:00:00'::timestamp without time zone)::timestamp(3) without time zone"},
	} {
		renderer.presentation = viewPresentation{deparse: c.deparse, pretty: c.pretty}
		got, err := renderer.expressionSQL(ctx, catalog, raw, key)
		if err != nil || got != c.want {
			t.Fatal("cast replay/presentation", c, got, err)
		}
	}
	outerPlan, err := sqltype.PlanCoercion(target, text.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	outerRaw := parser.CastExpr{Child: raw, TypeName: parser.TypeName{Name: "text"}}
	outerNode := coercionExpression{expressionInfo{usage: text.Usage()}, node, outerPlan, sqltype.Explicit}
	b.viewLiterals, b.viewCastUsages = nil, nil
	if err := b.captureViewLiterals(outerRaw, outerNode, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer = viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID(), presentation: viewPresentation{deparse: true, pretty: true}}
	got, err := renderer.expressionSQL(ctx, catalog, outerRaw, key)
	want := "(('2000-01-01 00:00:00'::timestamp without time zone)::timestamp(3) without time zone)::text"
	if err != nil || got != want {
		t.Fatal("nested cast grouping", got, want, err)
	}
}

func TestDatetimeFormattingViewOIDCastReplay(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	raw := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "42"}}, TypeName: parser.TypeName{Name: "oid"}}
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID()}
	for _, c := range []struct {
		deparse bool
		want    string
	}{
		{false, "CAST('42' AS OID)"}, {true, "'42'::oid"},
	} {
		renderer.presentation = viewPresentation{deparse: c.deparse, pretty: true}
		got, err := renderer.expressionSQL(ctx, e.CatalogRead(), raw, viewLiteralPath{clause: viewTargetLiteral})
		if err != nil || got != c.want {
			t.Fatal("OID literal acquired duplicate cast", c, got, err)
		}
	}
}

func TestDatetimeFormattingCapturedLiteralGrouping(t *testing.T) {
	ctx := context.Background()
	literal := func(value parser.Value) parser.Expr { return parser.LiteralExpr{Value: value} }
	for _, c := range []struct {
		source parser.Expr
		want   string
	}{
		{literal(parser.Value{Kind: parser.StringValue, Text: "a"}), "('a'::text)::text"},
		{literal(parser.Value{Kind: parser.NullValue}), "(NULL::text)::text"},
		{literal(parser.Value{Kind: parser.IntegerValue, Integer: 1}), "1::text"},
		{literal(parser.Value{Kind: parser.IntegerValue, Integer: -1}), "('-1'::integer)::text"},
		{literal(parser.Value{Kind: parser.BooleanValue, Boolean: true}), "true::text"},
	} {
		e := New()
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := parser.CastExpr{Child: c.source, TypeName: parser.TypeName{Name: "text"}}
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
			t.Fatal(err)
		}
		renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID(), presentation: viewPresentation{deparse: true, pretty: true}}
		got, err := renderer.expressionSQL(ctx, e.CatalogRead(), raw, viewLiteralPath{clause: viewTargetLiteral})
		if err != nil || got != c.want {
			t.Fatal("annotated versus genuinely bare literal", c, got, err)
		}
	}
	// Function arguments retain the established pretty nested-cast flattening.
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "to_date"}}}, Args: []parser.Expr{
		parser.CastExpr{Child: literal(parser.Value{Kind: parser.StringValue, Text: "a"}), TypeName: parser.TypeName{Name: "text"}},
		literal(parser.Value{Kind: parser.StringValue, Text: "YYYY"}),
	}}
	node, err := b.bindDatetimeFormatting(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID(), presentation: viewPresentation{deparse: true, pretty: true}}
	got, err := renderer.expressionSQL(ctx, e.CatalogRead(), raw, viewLiteralPath{clause: viewTargetLiteral})
	if err != nil || got != "to_date('a'::text::text, 'YYYY'::text)" {
		t.Fatal("function argument cast grouping", got, err)
	}
}
