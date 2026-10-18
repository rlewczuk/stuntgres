package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArraySearchViewCoercions(t *testing.T) {
	ctx := context.Background()
	sourceEngine, targetEngine := New(), New()
	publishCollation := func(e *Engine, id ids.OID, name string) *collationDescriptor {
		d := collationUnitDefinition(e, id, name)
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
		collationUnitPublish(t, e, stage)
		return d
	}
	sourceCollation := publishCollation(sourceEngine, 71000, "as_unit_source")
	targetCollation := publishCollation(targetEngine, 72000, "as_unit_target")
	b := newTestExpressionBinder(t, ctx, sourceEngine, sqltype.InputEnvironment{}, nil)
	targetBinder := newTestExpressionBinder(t, ctx, targetEngine, sqltype.InputEnvironment{}, nil)
	integer, wide, text := b.typ(sqltype.Int4), b.typ(sqltype.Int8), b.typ(sqltype.Text)
	array, wideArray, textArray := b.typ(integer.AssociatedArray()), b.typ(wide.AssociatedArray()), b.typ(text.AssociatedArray())
	literal := func(value sqltype.Value) BoundExpr {
		return literalExpression{expressionInfo: b.info(value.Type().Usage(), parser.Position{}, ExprProperties{}), value: value}
	}
	coerce := func(child BoundExpr, target *sqltype.Descriptor, mode sqltype.CoercionContext) BoundExpr {
		plan, err := sqltype.PlanCoercion(child.ResultUsage(), target.Usage(), mode)
		if err != nil {
			t.Fatal(err)
		}
		return coercionExpression{expressionInfo: b.info(target.Usage(), parser.Position{}, coercionProperties(child, plan)), child: child, plan: plan, mode: mode}
	}
	one, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := array.Input(ctx, "{1,2}", -1)
	if err != nil {
		t.Fatal(err)
	}
	rawArray := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "{1,2}"}}, TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{-1}}}
	arrayArgument := coerce(coerce(literal(payload), array, sqltype.Explicit), wideArray, sqltype.Implicit)
	rawOne := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	rawWide := parser.CastExpr{Child: rawOne, TypeName: parser.TypeName{Name: "bigint"}}
	wideOne := coerce(literal(one), wide, sqltype.Explicit)
	name := func(text string) parser.ObjectName {
		return parser.ObjectName{Parts: []parser.Identifier{{Text: text}}}
	}
	inner, err := b.newArraySearch("array_position", name("array_position"), parser.Position{}, integer.Usage(), []BoundExpr{arrayArgument, wideOne})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := b.newArraySearch("array_replace", name("array_replace"), parser.Position{}, wideArray.Usage(), []BoundExpr{arrayArgument, wideOne, coerce(inner, wide, sqltype.Implicit)})
	if err != nil {
		t.Fatal(err)
	}
	raw := parser.FunctionExpr{Name: name("array_replace"), Args: []parser.Expr{rawArray, rawWide, parser.FunctionExpr{Name: name("array_position"), Args: []parser.Expr{rawArray, rawWide}}}}
	ref, err := sqltype.RegisteredCollation(uint32(sourceCollation.oid), sourceCollation.semantics)
	if err != nil {
		t.Fatal(err)
	}
	strings, err := textArray.Input(ctx, "{a,b,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	usage := textArray.Usage()
	usage.Collation = ref
	position := parser.Position{Character: 1}
	info := b.info(usage, position, ExprProperties{})
	info.collationExplicit = position
	collated := collationExpression{expressionInfo: info, child: literal(strings)}
	search, replacement := literal(sqltype.String(text, "a")), literal(sqltype.String(text, "b"))
	replace, err := b.newArraySearch("array_replace", name("array_replace"), parser.Position{}, textArray.Usage(), []BoundExpr{collated, search, replacement})
	if err != nil {
		t.Fatal(err)
	}
	positions, err := b.newArraySearch("array_positions", name("array_positions"), parser.Position{}, array.Usage(), []BoundExpr{replace, replacement})
	if err != nil {
		t.Fatal(err)
	}
	rawText := parser.FunctionExpr{Name: name("array_positions"), Args: []parser.Expr{
		parser.FunctionExpr{Name: name("array_replace"), Args: []parser.Expr{
			parser.CollateExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "{a,b,NULL}"}}, Name: name("as_unit_source"), Pos: position},
			parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "a"}},
			parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "b"}},
		}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "b"}},
	}}
	for i, capture := range []struct {
		raw  parser.Expr
		node BoundExpr
	}{{raw, outer}, {rawText, positions}} {
		if err := b.captureViewLiterals(capture.raw, capture.node, viewTargetLiteral, i); err != nil {
			t.Fatal(err)
		}
	}
	syntax := &parser.Select{Targets: []parser.Expr{raw, rawText}}
	project := &projectNode{querySchema: querySchema{{Name: "r", Usage: wideArray.Usage()}, {Name: "ps", Usage: array.Usage()}}, child: &singletonNode{}, expressions: []BoundExpr{outer, positions}, frame: b.frameID(), syntax: syntax}
	original := &viewDefinition{query: &BoundQuery{Root: project}, syntax: syntax, literals: b.viewLiterals, castUsages: b.viewCastUsages, collations: b.viewCollations}
	path := []ids.ObjectAddress{{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}, {ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}}
	render := func(view CatalogRead, definition *viewDefinition, target int, expression parser.Expr, want string) {
		r := viewRenderer{viewDefinition: definition, frame: b.frameID(), presentation: viewPresentation{deparse: true, pretty: true, path: path}}
		got, err := r.expressionSQL(ctx, view, expression, viewLiteralPath{clause: viewTargetLiteral, target: target})
		if err != nil || got != want {
			t.Fatalf("rendered %q want %q: %v", got, want, err)
		}
	}
	const numericSQL = "array_replace('{1,2}'::integer[]::integer[]::bigint[], 1::bigint, array_position('{1,2}'::integer[]::integer[]::bigint[], 1::bigint)::bigint)"
	const sourceTextSQL = "array_positions(array_replace(('{a,b,NULL}'::text[] COLLATE as_unit_source), 'a'::text, 'b'::text), 'b'::text)"
	const targetTextSQL = "array_positions(array_replace(('{a,b,NULL}'::text[] COLLATE as_unit_target), 'a'::text, 'b'::text), 'b'::text)"
	assertValue := func(node BoundExpr, typ *sqltype.Descriptor, want string) sqltype.Value {
		value, err := node.Eval(&EvalContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		got, err := typ.Output(ctx, value)
		if err != nil || value.Type() != typ || got != want {
			t.Fatalf("value %q/%v want %q/%v: %v", got, value.Type(), want, typ, err)
		}
		for i := range value.ArrayLen() {
			if value.ArrayElement(i).Type() != typ.Element() {
				t.Fatal("foreign output leaf generation")
			}
		}
		return value
	}
	sourceView, targetView := sourceEngine.CatalogRead(), targetEngine.CatalogRead()
	render(sourceView, original, 0, raw, numericSQL)
	render(sourceView, original, 1, rawText, sourceTextSQL)
	oldNumeric := assertValue(outer, wideArray, "{1,2}")
	oldPositions := assertValue(positions, array, "{1,2}")
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{sourceCollation.Address(): targetCollation.Address()}
	collation := remappedCollationCapabilities(targetView.state, mapping)
	types := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		return targetBinder.typ(d.ID())
	}
	identity := func(ref ids.ObjectRef) ids.ObjectRef { return ref }
	mapped, err := remapFrozenPresentation(targetView, original, mapping, identity, types, collation)
	if err != nil {
		t.Fatal(err)
	}
	mappedProject := mapped.query.Root.(*projectNode)
	if mappedProject == project || mappedProject.Schema()[0].Usage.Type == wideArray || mappedProject.Schema()[1].Usage.Type == array {
		t.Fatal("descriptor transition did not produce target-owned schema")
	}
	render(targetView, mapped, 0, raw, numericSQL)
	render(targetView, mapped, 1, rawText, targetTextSQL)
	assertValue(mappedProject.expressions[0], targetBinder.typ(wideArray.ID()), "{1,2}")
	assertValue(mappedProject.expressions[1], targetBinder.typ(array.ID()), "{1,2}")
	newPlan := mappedProject.expressions[1].(functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]).Plan
	newRef, conflict := newPlan.InputCollation()
	if conflict || !newRef.Valid() || newRef.ID != uint32(targetCollation.oid) {
		t.Fatal("retained stale comparison collation", newRef, conflict)
	}
	// Re-render and execute the original after the real remap: its presentation,
	// scalar/array generations, equality authority and owned rows stay intact.
	render(sourceView, original, 0, raw, numericSQL)
	render(sourceView, original, 1, rawText, sourceTextSQL)
	assertValue(outer, wideArray, "{1,2}")
	assertValue(positions, array, "{1,2}")
	oldRef, conflict := positions.(functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]).Plan.InputCollation()
	if conflict || oldRef.ID != uint32(sourceCollation.oid) || oldNumeric.Type() != wideArray || oldPositions.Type() != array || project.Schema()[0].Usage.Type != wideArray {
		t.Fatal("remap mutated original authority or exported values")
	}
}
