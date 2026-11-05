package schemacmds_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const xml2XSLTStyle = `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:output method="text"/><xsl:template match="/"><xsl:value-of select="/r"/></xsl:template></xsl:stylesheet>`

func TestXML2XSLTExpression(t *testing.T) {
	e, ext := xml2UnitInstalled(t)
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for _, tc := range []struct {
		slot, count     int
		signature, tail string
		volatility      expr.Volatility
	}{
		{10, 3, "25,25,25", ",''", expr.Volatile},
		{11, 2, "25,25", "", expr.Immutable},
	} {
		// A binding frame owns one SQL root's position-keyed stored syntax.
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT xslt_process('<r>owned</r>','"+xml2XSLTStyle+"'"+tc.tail+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(plan.InstalledXML2Expr)
		p := e.CatalogRead().MustProcedure(ext.Members[tc.slot])
		immutable := tc.volatility == expr.Immutable
		if !analyze.ValidInstalledXML2Shape(n) || n.Procedure != p || n.RoutineReference != p || n.Ref.Address != p.Address() || p.Signature != catalog.ProcedureSignature(tc.signature) || n.Operation != functions.XML2XSLT || len(n.Args) != tc.count || n.IntrinsicVolatility() != tc.volatility || n.Properties().Volatility != tc.volatility || plan.GenerationImmutable(n) != immutable || exec.ConditionalConstantInputs(n) != immutable {
			t.Fatal("selected overload authority/fold protocol", n)
		}
		member, ok := functions.XML2Lookup("xslt_process", tc.count)
		if !ok || member.Signature != tc.signature || member.Operation != functions.XML2XSLT || member.Kernel != -1 || member.Volatility != tc.volatility {
			t.Fatal("exact member selection", member)
		}
		for _, reconstruct := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return n.Rebuild(n.Info, n.Args) },
			func() (expr.Expr, error) { return n.Substitute(n.Info, n.Args) },
			func() (expr.Expr, error) {
				x, _, err := n.Retag(n.Info, n.Args, true, expr.Retag{})
				return x, err
			},
		} {
			x, err := reconstruct()
			got, ok := x.(plan.InstalledXML2Expr)
			if err != nil || !ok || got.Operation != functions.XML2XSLT || len(got.Args) != tc.count || got.Procedure != p || got.RoutineReference != p || got.Ref != n.Ref || !analyze.SameGroupExpression(n, got) {
				t.Fatal("reconstruction erased selected overload", x, err)
			}
		}
		if err := b.CaptureViewLiterals(raw, n, analyze.ViewTargetLiteral, tc.slot); err != nil {
			t.Fatal(err)
		}
		grouped, err := b.RewriteGroup(n, nil)
		g, ok := grouped.(plan.InstalledXML2Expr)
		if err != nil || !ok || g.Operation != functions.XML2XSLT || len(g.Args) != tc.count || g.Procedure != p || !g.Properties().StageDependent || !analyze.ValidInstalledXML2Shape(g) {
			t.Fatal("grouping lost overload", grouped, err)
		}
		for _, simplify := range []func() (expr.Expr, bool, error){
			func() (expr.Expr, bool, error) { return exec.PreflightCoercionsWithPolicy(ctx, n, false) },
			func() (expr.Expr, bool, error) { return exec.SimplifyConditional(ctx, n, false) },
		} {
			x, _, err := simplify()
			if err != nil {
				t.Fatal(err)
			}
			if immutable {
				v, ok := x.(plan.LiteralExpr)
				if !ok {
					t.Fatal("immutable XSLT did not fold", x)
				}
				text, textErr := v.Value.Text()
				if textErr != nil || text != "owned" || v.RoutineReference != nil {
					t.Fatal("immutable result fold", x)
				}
			} else if got, ok := x.(plan.InstalledXML2Expr); !ok || got.Operation != functions.XML2XSLT || len(got.Args) != tc.count || got.Procedure != p {
				t.Fatal("VOLATILE three-argument call folded", x)
			}
		}
		for _, mutate := range []func(*plan.InstalledXML2Expr){
			func(x *plan.InstalledXML2Expr) { x.Operation = functions.XML2String },
			func(x *plan.InstalledXML2Expr) {
				if immutable {
					x.Args = append(append([]expr.Expr(nil), x.Args...), x.Args[1])
				} else {
					x.Args = x.Args[:2]
				}
			},
			func(x *plan.InstalledXML2Expr) {
				otherSlot := 10
				if tc.slot == 10 {
					otherSlot = 11
				}
				x.Procedure = e.CatalogRead().MustProcedure(ext.Members[otherSlot])
				x.RoutineReference = x.Procedure
			},
			func(x *plan.InstalledXML2Expr) { x.RoutineReference = nil },
		} {
			bad := n
			mutate(&bad)
			if analyze.ValidInstalledXML2Shape(bad) {
				t.Fatal("forged overload accepted", bad)
			}
		}
		for nullAt := range n.Args {
			children := append([]expr.Expr(nil), n.Args...)
			info := expr.InfoOf(children[nullAt])
			children[nullAt] = plan.LiteralExpr{Info: info, Value: sqltype.Null(info.Usage.Type)}
			rebuilt, err := n.Rebuild(n.Info, children)
			if err != nil {
				t.Fatal(err)
			}
			v, err := rebuilt.Eval(ctx)
			if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
				t.Fatal("strict operand", nullAt, v, err)
			}
		}
	}
	if _, ok := functions.XML2Lookup("xslt_process", 1); ok {
		t.Fatal("unreleased signature accepted")
	}
}

func TestXML2XSLTProvider(t *testing.T) {
	e, ext := xml2UnitInstalled(t)
	v := e.CatalogRead()
	for _, slot := range []int{10, 11} {
		p := v.MustProcedure(ext.Members[slot])
		if err := extension.StockXML2.ValidateProcedure(v, p); err != nil {
			t.Fatal(err)
		}
		for _, other := range []int{10, 11} {
			if other == slot {
				continue
			}
			bad := *p
			origin := *p.Origin
			origin.Declaration = other
			bad.Origin = &origin
			state := catalog.CloneState(v.State())
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockXML2.ValidateProcedure(v.WithState(state), &bad); err == nil {
				t.Fatal("sibling overload authenticated by spelling")
			}
		}
	}
}

func TestXML2XSLTStoredDefaults(t *testing.T) {
	ctx := context.Background()
	e, ext := xml2UnitInstalled(t)
	view := e.CatalogRead()
	bind := func(source, tail string) plan.InstalledXML2Expr {
		t.Helper()
		b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT xslt_process("+source+",'"+xml2XSLTStyle+"'"+tail+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		return node.(plan.InstalledXML2Expr)
	}
	for _, tc := range []struct {
		tail       string
		slot       int
		volatility expr.Volatility
	}{
		{"", 11, expr.Immutable},
		{",''", 10, expr.Volatile},
	} {
		call := bind("current_setting('application_name')", tc.tail)
		child := call.Args[0].(functions.Setting)
		before, err := child.Render()
		if err != nil || !before.Replay.RelationOnly || before.Replay.Validate() != nil {
			t.Fatal("original setting replay contract", before, err)
		}
		backing, childBacking := &call.Args[0], &child.Args[0]
		if call.Procedure != view.MustProcedure(ext.Members[tc.slot]) || call.IntrinsicVolatility() != tc.volatility || !analyze.ValidInstalledXML2Shape(call) || !analyze.ClosedPgcryptoDefault(call) || !analyze.ClosedPgstattupleDefault(call, view) {
			t.Fatal("authentic closed default authority", call)
		}
		text, dependencies, err := stored.RenderColumnDefault(ctx, view, call)
		if err != nil || !strings.Contains(text, `"public"."xslt_process"`) || !strings.Contains(text, "pg_catalog.current_setting(") || !slices.Contains(dependencies, call.Ref) {
			t.Fatal("closed statement-state child replay", text, dependencies, err)
		}
		after, err := child.Render()
		if err != nil || !before.Replay.RelationOnly || !after.Replay.RelationOnly || before.Replay.Validate() != nil || after.Replay.Validate() != nil || &call.Args[0] != backing || &child.Args[0] != childBacking || e.CatalogRead().State() != view.State() {
			t.Fatal("default rendering mutated original contract/backing", err)
		}
		for _, source := range []string{
			"set_config('application_name','<r/>',false)",
			"current_setting($1::text)",
		} {
			unclosed := bind(source, tc.tail)
			if analyze.ClosedPgcryptoDefault(unclosed) || analyze.ClosedPgstattupleDefault(unclosed, view) {
				t.Fatal("unrelated volatile/parameter producer gained closure", source)
			}
			if _, err := stored.DefaultSQL(ctx, view, unclosed); err == nil {
				t.Fatal("unclosed XSLT DEFAULT rendered", source)
			}
			if _, err := (stored.ScalarRenderer{}).ScalarFunctionSQL(ctx, view, unclosed.Args[0]); err == nil {
				t.Fatal("relation-only child gained generalized replay permission", source)
			}
		}
	}
}
