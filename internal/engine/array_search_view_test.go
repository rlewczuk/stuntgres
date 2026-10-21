package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArraySearchViewCoercions(t *testing.T) {
	ctx := context.Background()
	sourceEngine, targetEngine := New(), New()
	publishCollation := func(e *Engine, id ids.OID, name string) *catalog.Collation {
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
	integer, wide, text := b.Type(sqltype.Int4), b.Type(sqltype.Int8), b.Type(sqltype.Text)
	array, wideArray, textArray := b.Type(integer.AssociatedArray()), b.Type(wide.AssociatedArray()), b.Type(text.AssociatedArray())
	literal := func(value sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: b.Info(value.Type().Usage(), parser.Position{}, expr.Properties{}), Value: value}
	}
	coerce := func(child expr.Expr, target *sqltype.Descriptor, mode sqltype.CoercionContext) expr.Expr {
		coercionPlan, err := sqltype.PlanCoercion(child.ResultUsage(), target.Usage(), mode)
		if err != nil {
			t.Fatal(err)
		}
		return plan.CoercionExpr{Info: b.Info(target.Usage(), parser.Position{}, analyze.CoercionProperties(child, coercionPlan)), Child: child, Plan: coercionPlan, Mode: mode}
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
	inner, err := functions.ArraySearchNode(b, "array_position", name("array_position"), parser.Position{}, integer.Usage(), []expr.Expr{arrayArgument, wideOne})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := functions.ArraySearchNode(b, "array_replace", name("array_replace"), parser.Position{}, wideArray.Usage(), []expr.Expr{arrayArgument, wideOne, coerce(inner, wide, sqltype.Implicit)})
	if err != nil {
		t.Fatal(err)
	}
	raw := parser.FunctionExpr{Name: name("array_replace"), Args: []parser.Expr{rawArray, rawWide, parser.FunctionExpr{Name: name("array_position"), Args: []parser.Expr{rawArray, rawWide}}}}
	ref, err := sqltype.RegisteredCollation(uint32(sourceCollation.OID), sourceCollation.Semantics)
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
	info := b.Info(usage, position, expr.Properties{})
	info.CollationExplicit = position
	collated := plan.CollationExpr{Info: info, Child: literal(strings)}
	search, replacement := literal(sqltype.String(text, "a")), literal(sqltype.String(text, "b"))
	replace, err := functions.ArraySearchNode(b, "array_replace", name("array_replace"), parser.Position{}, textArray.Usage(), []expr.Expr{collated, search, replacement})
	if err != nil {
		t.Fatal(err)
	}
	positions, err := functions.ArraySearchNode(b, "array_positions", name("array_positions"), parser.Position{}, array.Usage(), []expr.Expr{replace, replacement})
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
		node expr.Expr
	}{{raw, outer}, {rawText, positions}} {
		if err := b.CaptureViewLiterals(capture.raw, capture.node, analyze.ViewTargetLiteral, i); err != nil {
			t.Fatal(err)
		}
	}
	syntax := &parser.Select{Targets: []parser.Expr{raw, rawText}}
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "r", Usage: wideArray.Usage()}, {Name: "ps", Usage: array.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{outer, positions}, Frame: b.FrameID(), Syntax: syntax}
	original := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: project}, Syntax: syntax, Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages, Collations: b.Capture.Collations})
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
	render := func(view catalog.Read, definition *stored.View, target int, expression parser.Expr, want string) {
		r := stored.QueryRenderer{View: definition, Frame: b.FrameID(), Presentation: stored.Presentation{Deparse: true, Pretty: true, Path: path}}
		got, err := r.ExpressionSQL(ctx, view, expression, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral, Target: target})
		if err != nil || got != want {
			t.Fatalf("rendered %q want %q: %v", got, want, err)
		}
	}
	const numericSQL = "array_replace('{1,2}'::integer[]::integer[]::bigint[], 1::bigint, array_position('{1,2}'::integer[]::integer[]::bigint[], 1::bigint)::bigint)"
	const sourceTextSQL = "array_positions(array_replace(('{a,b,NULL}'::text[] COLLATE as_unit_source), 'a'::text, 'b'::text), 'b'::text)"
	const targetTextSQL = "array_positions(array_replace(('{a,b,NULL}'::text[] COLLATE as_unit_target), 'a'::text, 'b'::text), 'b'::text)"
	assertValue := func(node expr.Expr, typ *sqltype.Descriptor, want string) sqltype.Value {
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
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
	collation := typedef.RemappedCollationCapabilities(targetView.State(), mapping)
	types := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		return targetBinder.Type(d.ID())
	}
	identity := func(ref ids.ObjectRef) ids.ObjectRef { return ref }
	mapped, err := stored.RemapView(targetView, original, mapping, identity, types, collation)
	if err != nil {
		t.Fatal(err)
	}
	mappedProject := mapped.Query().Root.(*plan.ProjectNode)
	if mappedProject == project || mappedProject.Schema()[0].Usage.Type == wideArray || mappedProject.Schema()[1].Usage.Type == array {
		t.Fatal("descriptor transition did not produce target-owned schema")
	}
	render(targetView, mapped, 0, raw, numericSQL)
	render(targetView, mapped, 1, rawText, targetTextSQL)
	assertValue(mappedProject.Expressions[0], targetBinder.Type(wideArray.ID()), "{1,2}")
	assertValue(mappedProject.Expressions[1], targetBinder.Type(array.ID()), "{1,2}")
	newPlan := mappedProject.Expressions[1].(functions.ArraySearch).Plan
	newRef, conflict := newPlan.InputCollation()
	if conflict || !newRef.Valid() || newRef.ID != uint32(targetCollation.OID) {
		t.Fatal("retained stale comparison collation", newRef, conflict)
	}
	// Re-render and execute the original after the real remap: its presentation,
	// scalar/array generations, equality authority and owned rows stay intact.
	render(sourceView, original, 0, raw, numericSQL)
	render(sourceView, original, 1, rawText, sourceTextSQL)
	assertValue(outer, wideArray, "{1,2}")
	assertValue(positions, array, "{1,2}")
	oldRef, conflict := positions.(functions.ArraySearch).Plan.InputCollation()
	if conflict || oldRef.ID != uint32(sourceCollation.OID) || oldNumeric.Type() != wideArray || oldPositions.Type() != array || project.Schema()[0].Usage.Type != wideArray {
		t.Fatal("remap mutated original authority or exported values")
	}
}
