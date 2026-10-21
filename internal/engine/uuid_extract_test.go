package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func uuidExtractEngineUnit(t *testing.T, b *analyze.Binder, kind functions.UUIDExtractKind) functions.UUIDExtract {
	t.Helper()
	d := b.Type(sqltype.UUID)
	value, err := d.Input(context.Background(), "00000000-0000-7000-8000-000000000000", -1)
	if err != nil {
		t.Fatal(err)
	}
	arg := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: value}
	node, err := functions.NewUUIDExtract(b.Info(b.Type(kind.Result()).Usage(), parser.Position{Character: 8}, expr.Properties{Volatility: expr.Immutable}), kind, expr.Expr(arg))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestUUIDExtractIdentity(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	version := uuidExtractEngineUnit(t, b, functions.UUIDExtractVersion)
	stamp := uuidExtractEngineUnit(t, b, functions.UUIDExtractTimestamp)
	if !analyze.SameGroupExpression(version, version) || analyze.SameGroupExpression(version, stamp) {
		t.Fatal("operation identity")
	}
	other := version
	v, err := b.Type(sqltype.UUID).Input(context.Background(), "00000000-0000-1000-8000-000000000000", -1)
	if err != nil {
		t.Fatal(err)
	}
	other.Arg = plan.LiteralExpr{Info: expr.Info{Usage: b.Type(sqltype.UUID).Usage()}, Value: v}
	if analyze.SameGroupExpression(version, other) {
		t.Fatal("child identity lost")
	}
	children := 0
	plan.ExpressionChildren(version, func(child expr.Expr) {
		children++
		if !analyze.SameGroupExpression(child, version.Arg) {
			t.Fatal("wrong child")
		}
	})
	if children != 1 {
		t.Fatal("unary traversal", children)
	}
	rewritten, err := b.RewriteGroup(version, []expr.Expr{version.Arg})
	if err != nil {
		t.Fatal(err)
	}
	next := rewritten.(functions.UUIDExtract)
	if _, ok := next.Arg.(plan.GroupExpr); !ok || !next.Properties().StageDependent {
		t.Fatal("group child/stage rewrite lost")
	}
	if _, ok := version.Arg.(plan.LiteralExpr); !ok {
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
		mapped, changed, err := retagBoundTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh.Type(d.ID()) })
		if err != nil || !changed {
			t.Fatal(mapped, changed, err)
		}
		next := mapped.(functions.UUIDExtract)
		if next.Validate() != nil || next.ResultUsage().Type != fresh.Type(kind.Result()) || next.Arg.ResultUsage().Type != fresh.Type(sqltype.UUID) {
			t.Fatal("stale descriptors")
		}
		if node.Arg.ResultUsage().Type != b.Type(sqltype.UUID) || node.ResultUsage().Type != b.Type(kind.Result()) {
			t.Fatal("mutated original")
		}
		v, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || v.Type() != fresh.Type(kind.Result()) {
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
		if !plan.GenerationImmutable(node) || exec.PlannedVolatility(node) != expr.Immutable || !exec.ConditionalConstantInputs(node) {
			t.Fatal("immutable lifecycle")
		}
		folded, changed, err := exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, node)
		if err != nil || !changed {
			t.Fatal(folded, changed, err)
		}
		v, err := folded.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || v.Type() != b.Type(kind.Result()) {
			t.Fatal("folded authority", v, err)
		}
		recipe, err := (scalarRenderer{}).scalarFunctionSQL(ctx, engine.CatalogRead(), node)
		if err != nil || !strings.Contains(recipe, "pg_catalog."+kind.Name()+"(") {
			t.Fatal(recipe, err)
		}
		bad := node
		bad.Kind = 0
		if _, err := (scalarRenderer{}).scalarFunctionSQL(ctx, engine.CatalogRead(), bad); err == nil {
			t.Fatal("invalid stored provider")
		}
	}
	// A UUID-producing volatile descendant remains visible through extraction.
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_extract_version"}}}, Args: []parser.Expr{parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuidv4"}}}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil || node.Properties().Volatility != expr.Volatile || exec.PlannedVolatility(node) != expr.Volatile || plan.GenerationImmutable(node) {
		t.Fatal("volatile child erased", node, err)
	}
	if !analyze.ClosedUUIDDefault(node) {
		t.Fatal("closed UUID traversal lost")
	}
}
