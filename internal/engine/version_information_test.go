package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func versionInformationUnitLeaf(t *testing.T, b *expressionBinder, kind functions.VersionInformationKind) functions.VersionInformation {
	t.Helper()
	node, err := b.bindFunction(parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: kind.Name()}}},
		Pos:  parser.Position{Offset: 7, Character: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf, ok := node.(functions.VersionInformation)
	if !ok {
		t.Fatalf("unexpected carrier %T", node)
	}
	return leaf
}

func TestVersionInformationBinding(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		kind       functions.VersionInformationKind
		oid        ids.OID
		volatility expr.Volatility
	}{
		{functions.VersionInformationServer, 89, expr.Stable},
		{functions.VersionInformationUnicode, 4549, expr.Immutable},
		{functions.VersionInformationICU, 6099, expr.Immutable},
	} {
		leaf := versionInformationUnitLeaf(t, b, tc.kind)
		if leaf.ResultUsage() != b.typ(sqltype.Text).Usage() || leaf.Property != (expr.Properties{Volatility: tc.volatility}) || leaf.RoutineReference == nil || leaf.RoutineReference.RoutineOID() != uint32(tc.oid) {
			t.Fatalf("incorrect binding: %#v", leaf)
		}
		declaration, err := lookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, tc.kind.Name(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if declaration.Identity.ID != tc.oid || declaration.Identity.Signature != "" || declaration.Kind != catalog.FunctionRoutine || declaration.Result != 25 || !declaration.Strict || declaration.Parallel != 's' || declaration.Volatility != tc.volatility || declaration.ReturnsSet || declaration.Variadic != 0 || declaration.Defaults != 0 {
			t.Fatalf("incorrect declaration %#v", declaration)
		}
		if plannedVolatility(leaf) != tc.volatility || generationImmutable(leaf) != (tc.volatility == expr.Immutable) {
			t.Error("lost immutable/stable boundary")
		}
		if sides, err := joinExpressionSides(leaf, 0, 0); err != nil || sides != 0 {
			t.Fatal("invented row source", sides, err)
		}
		if !b.sameGroupExpression(leaf, leaf) || !closedTransactionTimeDefault(leaf) {
			t.Error("lost group/default leaf participation")
		}
		other := leaf
		other.Kind = functions.VersionInformationUnicode
		if tc.kind != other.Kind && b.sameGroupExpression(leaf, other) {
			t.Error("distinct inquiries merged")
		}
		children := 0
		boundExpressionChildren(leaf, func(expr.Expr) { children++ })
		if children != 0 {
			t.Error("zero-argument leaf has children")
		}
	}
}

func TestVersionInformationRemap(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	for _, kind := range []functions.VersionInformationKind{functions.VersionInformationServer, functions.VersionInformationUnicode, functions.VersionInformationICU} {
		leaf := versionInformationUnitLeaf(t, b, kind)
		retagged, changed, err := retagBoundTypes(target.CatalogRead(), leaf, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := target.CatalogRead().LookupType(d.ID())
			return next
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		remapped, err := remapBoundExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef { t.Fatal("leaf invented row dependency", ref); return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		copy := remapped.(functions.VersionInformation)
		if copy.Kind != kind || copy.Pos != leaf.Pos || copy.Usage.Type == leaf.Usage.Type || copy.RoutineReference == nil || copy.RoutineReference.RoutineOID() != leaf.RoutineReference.RoutineOID() || copy.Validate(expr.PropertiesMatch(copy.Property, kind.IntrinsicRule())) != nil {
			t.Fatal("lost remap identity", leaf, copy)
		}
		value, err := copy.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil || value.Type() != copy.Usage.Type || value.IsNull() != (kind == functions.VersionInformationICU) {
			t.Fatal("lost result ownership", value, err)
		}
		want := kind.Name() + "()"
		text, err := (scalarRenderer{}).sql(ctx, target.CatalogRead(), copy)
		if err != nil || text != want {
			t.Fatal(text, err)
		}
		text, err = (pgRenderer{view: target.CatalogRead()}).sql(ctx, copy, pgRenderParent{})
		if err != nil || text != want {
			t.Fatal(text, err)
		}
		tree, err := (boundTree{expression: copy}).StructuralOutput(ctx)
		if err != nil || !strings.Contains(tree, "VersionInformation") || strings.Contains(tree, "Const") {
			t.Fatal(tree, err)
		}
	}
}
