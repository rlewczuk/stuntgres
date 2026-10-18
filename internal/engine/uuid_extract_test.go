package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func uuidExtractEngineUnit(t *testing.T, b *expressionBinder, kind functions.UUIDExtractKind) functions.UUIDExtract {
	t.Helper()
	d := b.typ(sqltype.UUID)
	value, err := d.Input(context.Background(), "00000000-0000-7000-8000-000000000000", -1)
	if err != nil {
		t.Fatal(err)
	}
	arg := literalExpression{expressionInfo: expressionInfo{Usage: d.Usage()}, value: value}
	node, err := functions.NewUUIDExtract(functionInfo(b.info(b.typ(kind.Result()).Usage(), parser.Position{Character: 8}, ExprProperties{Volatility: Immutable})), kind, BoundExpr(arg))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestUUIDExtractIdentity(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	version := uuidExtractEngineUnit(t, b, functions.UUIDExtractVersion)
	stamp := uuidExtractEngineUnit(t, b, functions.UUIDExtractTimestamp)
	if !sameGroupExpression(version, version) || sameGroupExpression(version, stamp) {
		t.Fatal("operation identity")
	}
	other := version
	v, err := b.typ(sqltype.UUID).Input(context.Background(), "00000000-0000-1000-8000-000000000000", -1)
	if err != nil {
		t.Fatal(err)
	}
	other.Arg = literalExpression{expressionInfo: expressionInfo{Usage: b.typ(sqltype.UUID).Usage()}, value: v}
	if sameGroupExpression(version, other) {
		t.Fatal("child identity lost")
	}
	children := 0
	boundExpressionChildren(version, func(child BoundExpr) {
		children++
		if !sameGroupExpression(child, version.Arg) {
			t.Fatal("wrong child")
		}
	})
	if children != 1 {
		t.Fatal("unary traversal", children)
	}
	rewritten, err := b.rewriteGroup(version, []BoundExpr{version.Arg})
	if err != nil {
		t.Fatal(err)
	}
	next := rewritten.(functions.UUIDExtract)
	if _, ok := next.Arg.(groupExpression); !ok || !next.Properties().StageDependent {
		t.Fatal("group child/stage rewrite lost")
	}
	if _, ok := version.Arg.(literalExpression); !ok {
		t.Fatal("rewrite mutated original child")
	}
}

func TestUUIDExtractRemap(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	fresh := newTestExpressionBinder(t, ctx, target, sqltype.InputEnvironment{}, nil)
	for _, kind := range []functions.UUIDExtractKind{functions.UUIDExtractTimestamp, functions.UUIDExtractVersion} {
		node := uuidExtractEngineUnit(t, b, kind)
		mapped, changed, err := retagBoundTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh.typ(d.ID()) })
		if err != nil || !changed {
			t.Fatal(mapped, changed, err)
		}
		next := mapped.(functions.UUIDExtract)
		if next.Validate() != nil || next.ResultUsage().Type != fresh.typ(kind.Result()) || next.Arg.ResultUsage().Type != fresh.typ(sqltype.UUID) {
			t.Fatal("stale descriptors")
		}
		if node.Arg.ResultUsage().Type != b.typ(sqltype.UUID) || node.ResultUsage().Type != b.typ(kind.Result()) {
			t.Fatal("mutated original")
		}
		v, err := next.Eval(&EvalContext{Context: ctx})
		if err != nil || v.Type() != fresh.typ(kind.Result()) {
			t.Fatal(v, err)
		}
	}
}

func TestUUIDExtractScalarLifecycle(t *testing.T) {
	ctx := context.Background()
	engine := New()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	for _, kind := range []functions.UUIDExtractKind{functions.UUIDExtractTimestamp, functions.UUIDExtractVersion} {
		node := uuidExtractEngineUnit(t, b, kind)
		if !generationImmutable(node) || plannedVolatility(node) != Immutable || !conditionalConstantInputs(node) {
			t.Fatal("immutable lifecycle")
		}
		folded, changed, err := preflightCoercions(&EvalContext{Context: ctx}, node)
		if err != nil || !changed {
			t.Fatal(folded, changed, err)
		}
		v, err := folded.Eval(&EvalContext{Context: ctx})
		if err != nil || v.Type() != b.typ(kind.Result()) {
			t.Fatal("folded authority", v, err)
		}
		recipe, err := (scalarRenderer{}).scalarFunctionSQL(ctx, engine.CatalogRead(), node, node.ResultUsage())
		if err != nil || !strings.Contains(recipe, "pg_catalog."+kind.Name()+"(") {
			t.Fatal(recipe, err)
		}
		bad := node
		bad.Kind = 0
		if _, err := (scalarRenderer{}).scalarFunctionSQL(ctx, engine.CatalogRead(), bad, bad.ResultUsage()); err == nil {
			t.Fatal("invalid stored provider")
		}
	}
	// A UUID-producing volatile descendant remains visible through extraction.
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_extract_version"}}}, Args: []parser.Expr{parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuidv4"}}}}}}
	node, err := b.bindUUIDExtract(raw)
	if err != nil || node.Properties().Volatility != Volatile || plannedVolatility(node) != Volatile || generationImmutable(node) {
		t.Fatal("volatile child erased", node, err)
	}
	if !closedUUIDDefault(node) {
		t.Fatal("closed UUID traversal lost")
	}
}
