package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSSLInfoExpressionProtocol(t *testing.T) {
	e, ext := sslInfoUnitInstalled(t)
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for slot, name := range []string{"ssl_client_serial", "ssl_is_used", "ssl_version", "ssl_cipher", "ssl_client_cert_present", "ssl_client_dn", "ssl_issuer_dn"} {
		t.Run(name, func(t *testing.T) {
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.InstalledSSLInfoExpr)
			if n.Procedure != e.CatalogRead().MustProcedure(ext.Members[slot]) || n.Ref.Address != ext.Members[slot] || n.RoutineReference != n.Procedure || n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || !analyze.ValidInstalledSSLInfoShape(n) || !analyze.ClosedSSLInfoDefault(n) || plan.GenerationImmutable(n) || plan.GenerationHasTableOID(n) || exec.ConditionalConstantInputs(n) {
				t.Fatal("installed protocol", n)
			}
			// A constant outcome is not proof of capture: require its real revision
			// dependency and immutable released backing independently.
			found := false
			for _, dependency := range b.Dependencies {
				if dependency.Ref == n.Ref && dependency.Revision == n.Procedure.Revision() {
					found = true
				}
			}
			if !found || n.Procedure.Origin.Installation != ext.Installation || ext.Installation.Declarations[slot] == nil {
				t.Fatal("missing declaration backing/dependency")
			}
			if descriptor, err := exec.ExpressionRoutineDescriptor(n); err != nil || descriptor != n.Procedure {
				t.Fatal("routine capture", descriptor, err)
			}
			if admitted, set := n.SourceAdmission(); !admitted || set || len(n.Children()) != 0 {
				t.Fatal("not a scalar zero-child source")
			}
			value, err := n.Eval(ctx)
			if err != nil || value.Type() != n.Usage.Type {
				t.Fatal("typed result", value, err)
			}
			if slot == 1 || slot == 4 {
				if boolean, err := value.Boolean(); err != nil || boolean || value.IsNull() {
					t.Fatal("non-TLS false", value, err)
				}
			} else if !value.IsNull() {
				t.Fatal("fabricated TLS/certificate", value)
			}
			source, err := n.OpenSource(ctx)
			if err != nil {
				t.Fatal(err)
			}
			first, present, err := source.Next(context.Background())
			if err != nil || !present || first.Type() != value.Type() || first.IsNull() != value.IsNull() {
				t.Fatal("scalar first row", first, present, err)
			}
			if _, present, err := source.Next(context.Background()); err != nil || present {
				t.Fatal("scalar emitted more than one row", present, err)
			}
			source.Close()
			info := n.Info
			info.Pos.Offset++
			for _, rebuild := range []func() (expr.Expr, error){
				func() (expr.Expr, error) { return n.Rebuild(info, nil) },
				func() (expr.Expr, error) { return n.Substitute(info, nil) },
				func() (expr.Expr, error) { next, _, err := n.Retag(info, nil, true, expr.Retag{}); return next, err },
			} {
				rebuilt, err := rebuild()
				if err != nil {
					t.Fatal(err)
				}
				got, ok := rebuilt.(plan.InstalledSSLInfoExpr)
				if !ok || got.Procedure != n.Procedure || got.Ref != n.Ref || got.RoutineReference != n.Procedure || got.Info != info || !analyze.SameGroupExpression(n, got) {
					t.Fatal("wrapper reconstruction", rebuilt)
				}
			}
			if _, err := n.Rebuild(n.Info, []expr.Expr{n}); err == nil {
				t.Fatal("accepted a child")
			}
			bad := n
			bad.Name = "ssl_absent_inquiry"
			if bad.Validate() == nil || analyze.ValidInstalledSSLInfoShape(bad) {
				t.Fatal("invalid selector")
			}
			bad = n
			bad.Usage = b.Type(sqltype.Int4).Usage()
			if bad.Validate() == nil {
				t.Fatal("invalid result usage")
			}
			if analyze.ClosedSSLInfoDefault(n.SSLInfo) {
				t.Fatal("bare selector granted DEFAULT authority")
			}
			if err := b.CaptureViewLiterals(raw, n, analyze.ViewTargetLiteral, slot); err != nil {
				t.Fatal("authentic zero-child capture", err)
			}
			bad = n
			bad.RoutineReference = nil
			if err := b.CaptureViewLiterals(raw, bad, analyze.ViewTargetLiteral, slot); err == nil {
				t.Fatal("capture lost installed authentication")
			}
			grouped, err := b.RewriteGroup(n, nil)
			if err != nil {
				t.Fatal(err)
			}
			g := grouped.(plan.InstalledSSLInfoExpr)
			if g.Procedure != n.Procedure || g.Ref != n.Ref || !g.Properties().StageDependent || !analyze.ValidInstalledSSLInfoShape(g) || analyze.ClosedSSLInfoDefault(g) {
				t.Fatal("group reconstruction", g)
			}
			for _, rewrite := range []func() (expr.Expr, bool, error){
				func() (expr.Expr, bool, error) { return exec.PreflightCoercionsWithPolicy(ctx, n, false) },
				func() (expr.Expr, bool, error) { return exec.SimplifyConditional(ctx, n, false) },
			} {
				next, _, err := rewrite()
				got, ok := next.(plan.InstalledSSLInfoExpr)
				if err != nil || !ok || got.Procedure != n.Procedure || got.Ref != n.Ref {
					t.Fatal("volatile wrapper folded", next, err)
				}
			}
			copy := n
			copy.Ref.Epoch++
			if analyze.SameGroupExpression(n, copy) {
				t.Fatal("group ignored reference identity")
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	p := e.CatalogRead().MustProcedure(ext.Members[0])
	node, err := b.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: p.Name()}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: cancelled}}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	// Metadata-only impostors cannot masquerade as installed recipes.
	bad := node.(plan.InstalledSSLInfoExpr)
	bad.Procedure = &catalog.Procedure{}
	if analyze.ValidInstalledSSLInfoShape(bad) {
		t.Fatal("impostor shape")
	}
	t.Run("MixedDefaults", func(t *testing.T) {
		call := func(name string) parser.Expr {
			return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
		}
		for _, producer := range []string{"uuidv4", "random"} {
			row, err := b.Bind(parser.RowExpr{Fields: []parser.Expr{call("ssl_is_used"), call(producer)}, Explicit: true}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if analyze.ClosedSSLInfoDefault(row) != (producer == "uuidv4") {
				t.Fatal("mixed tree bypassed independent producer admission", producer, row)
			}
		}
		plain, err := b.Bind(parser.RowExpr{Fields: []parser.Expr{call("uuidv4")}, Explicit: true}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if analyze.ClosedSSLInfoDefault(plain) {
			t.Fatal("sslinfo policy claimed a tree without sslinfo")
		}
	})
}
