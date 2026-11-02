package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestXML2ExpressionIdentity(t *testing.T) {
	e, ext := xml2UnitInstalled(t)
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for slot, query := range []string{"SELECT xml_valid('<r/>')", "SELECT xpath_string('<r>12</r>','.')", "SELECT xpath_number('<r>12</r>','.')", "SELECT xpath_bool('<r>12</r>','.')"} {
		raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
		node, err := b.BindFunctionProvider(raw.(parser.FunctionExpr))
		if err != nil {
			t.Fatal(err)
		}
		n := node.(plan.InstalledXML2Expr)
		if !analyze.ValidInstalledXML2Shape(n) || n.Procedure != e.CatalogRead().MustProcedure(ext.Members[slot]) || n.RoutineReference != n.Procedure || n.Ref.Address != ext.Members[slot] || n.Procedure.Origin.Installation != ext.Installation {
			t.Fatal("authenticated recipe", n)
		}
		preflightKind := expr.PreflightSimplify
		if slot == 0 {
			preflightKind = expr.PreflightChildren
		}
		if plan.GenerationImmutable(n) != (slot != 0) || exec.ConditionalConstantInputs(n) != (slot != 0) || n.PreflightFold() != preflightKind {
			t.Fatal("volatility/fold protocol", n)
		}
		found := false
		for _, d := range b.Dependencies {
			if d.Ref == n.Ref && d.Revision == n.Procedure.Revision() {
				found = true
			}
		}
		if !found {
			t.Fatal("missing provider dependency")
		}
		info := n.Info
		info.Pos.Offset++
		for _, rebuild := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return n.Rebuild(info, n.Args) },
			func() (expr.Expr, error) { return n.Substitute(info, n.Args) },
			func() (expr.Expr, error) { next, _, err := n.Retag(info, n.Args, true, expr.Retag{}); return next, err },
		} {
			next, err := rebuild()
			if err != nil {
				t.Fatal(err)
			}
			got, ok := next.(plan.InstalledXML2Expr)
			if !ok || got.Procedure != n.Procedure || got.Ref != n.Ref || got.RoutineReference != n.Procedure || got.Info != info || !analyze.SameGroupExpression(n, got) {
				t.Fatal("identity-erasing reconstruction", next)
			}
		}
		if _, err := n.Rebuild(n.Info, nil); err == nil {
			t.Fatal("invalid arity accepted")
		}
		if err := b.CaptureViewLiterals(raw, n, analyze.ViewTargetLiteral, slot); err != nil {
			t.Fatal("capture", err)
		}
		bad := n
		bad.RoutineReference = nil
		if analyze.ValidInstalledXML2Shape(bad) || b.CaptureViewLiterals(raw, bad, analyze.ViewTargetLiteral, slot) == nil {
			t.Fatal("unauthenticated capture")
		}
		bad = n
		bad.Ref.Epoch++
		if analyze.SameGroupExpression(n, bad) {
			t.Fatal("group ignored generation")
		}
		bad = n
		bad.Name = "xpath_nodeset"
		if bad.Validate() == nil {
			t.Fatal("adjacent selector accepted")
		}
		grouped, err := b.RewriteGroup(n, nil)
		if err != nil {
			t.Fatal(err)
		}
		g := grouped.(plan.InstalledXML2Expr)
		if g.Procedure != n.Procedure || g.Ref != n.Ref || !g.Properties().StageDependent || !analyze.ValidInstalledXML2Shape(g) || len(g.Args) != len(n.Args) {
			t.Fatal("group recipe", g)
		}
		preflight, _, err := exec.PreflightCoercionsWithPolicy(ctx, n, false)
		if err != nil {
			t.Fatal(err)
		}
		// This is execution preflight, not Parse/Describe. Real prepared-owner
		// units separately require an empty fold publication before Bind.
		if slot == 0 {
			if got, ok := preflight.(plan.InstalledXML2Expr); !ok || got.Procedure != n.Procedure || got.Ref != n.Ref {
				t.Fatal("STABLE preflight erased recipe", preflight)
			}
		} else {
			if got, ok := preflight.(plan.LiteralExpr); !ok || got.Value.Type() != n.Usage.Type || got.RoutineReference != nil {
				t.Fatal("execution preflight did not fold installed constant", preflight)
			}
		}
		folded, _, err := exec.SimplifyConditional(ctx, n, false)
		if err != nil {
			t.Fatal(err)
		}
		if slot == 0 {
			if _, ok := folded.(plan.InstalledXML2Expr); !ok {
				t.Fatal("STABLE validator folded")
			}
		} else {
			v, ok := folded.(plan.LiteralExpr)
			if !ok || v.Value.Type() != n.Usage.Type || v.RoutineReference != nil {
				t.Fatal("immutable scalar not folded", folded)
			}
		}
		if slot != 0 {
			children := append([]expr.Expr(nil), n.Args...)
			pathInfo := expr.InfoOf(children[1])
			children[1] = plan.LiteralExpr{Info: pathInfo, Value: sqltype.String(pathInfo.Usage.Type, "[")}
			malformed, err := n.Rebuild(n.Info, children)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = exec.PreflightCoercionsWithPolicy(ctx, malformed, false)
			state, ok := err.(interface{ SQLState() string })
			if !ok || state.SQLState() != "10608" {
				t.Fatal("execution preflight skipped malformed immutable call", slot, err)
			}
		}
		for nullAt := range n.Args {
			children := append([]expr.Expr(nil), n.Args...)
			for i, child := range children {
				info := expr.InfoOf(child)
				if i == nullAt {
					children[i] = plan.LiteralExpr{Info: info, Value: sqltype.Null(info.Usage.Type)}
				} else {
					input := "<r>"
					if i == 1 {
						input = "["
					}
					children[i] = plan.LiteralExpr{Info: info, Value: sqltype.String(info.Usage.Type, input)}
				}
			}
			rebuilt, err := n.Rebuild(n.Info, children)
			if err != nil {
				t.Fatal(err)
			}
			value, err := rebuilt.Eval(ctx)
			if err != nil || value.Type() != n.Usage.Type || !value.IsNull() {
				t.Fatal("per-operand SQL NULL bypassed strictness", slot, nullAt, value, err)
			}
			planned, _, err := exec.PreflightCoercionsWithPolicy(ctx, rebuilt, false)
			literal, ok := planned.(plan.LiteralExpr)
			if err != nil || !ok || literal.Value.Type() != n.Usage.Type || !literal.Value.IsNull() {
				t.Fatal("execution strict NULL publication", slot, nullAt, planned, err)
			}
		}
	}
}
