package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestCitextPatternStoredIdentity(t *testing.T) {
	e, ext, base := citextUnitInstalled(t)
	view := e.CatalogRead()
	result, _ := view.LookupType(sqltype.Bool)
	text, _ := view.LookupType(sqltype.Text)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: base.SQLType.Usage()}, Value: sqltype.String(base.SQLType, "ABC")}
	for kind := range extension.CitextPatternNames {
		p := view.MustProcedure(ext.Members[extension.CitextPatternProcedureStart+kind*2])
		comparison, err := sqltype.BindCitextPattern(uint8(kind), base.SQLType.Usage(), base.SQLType.Usage(), result.Usage(), text.Usage(), base.SQLType.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		n := plan.CitextPatternExpr{Info: expr.Info{Usage: result.Usage(), RoutineReference: p}, Ref: view.Owner().Domain.Ref(view.Epoch(), p.Address()), Args: [2]expr.Expr{literal, literal}, Pattern: comparison, Text: text.Usage(), Kind: uint8(kind), Namespace: "public", Collation: base.SQLType.Usage().Collation}
		if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, n); err != nil {
			t.Fatal(err)
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 2 {
			t.Fatal(capture, ok, err)
		}
		for _, corrupt := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Signature = "25,25" },
			func(p *catalog.Procedure) { p.Strict = false },
			func(p *catalog.Procedure) { p.Implementation = extension.StockCube },
		} {
			s := catalog.CloneState(view.State())
			bad := *p
			corrupt(&bad)
			s.SetDescriptor(bad.Address(), &bad)
			if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view.WithState(s), n); err == nil {
				t.Fatal("refreshed immutable capture from substituted declaration")
			}
		}
		if n.RoutineReference != p || p.Signature != extension.CitextPatternSignature(base.SQLType.ID(), base.SQLType.ID()) {
			t.Fatal("source capture mutated")
		}
	}
}

func TestCitextPatternRebuild(t *testing.T) {
	e, ext, base := citextUnitInstalled(t)
	view := e.CatalogRead()
	result, _ := view.LookupType(sqltype.Bool)
	text, _ := view.LookupType(sqltype.Text)
	for kind := uint8(0); kind < 4; kind++ {
		for pair := 0; pair < 2; pair++ {
			right := base.SQLType
			if pair == 1 {
				right = text
			}
			args := [2]expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: base.SQLType.Usage()}, Value: sqltype.String(base.SQLType, "ABC")}, plan.LiteralExpr{Info: expr.Info{Usage: right.Usage()}, Value: sqltype.String(right, "abc")}}
			p := view.MustProcedure(ext.Members[extension.CitextPatternProcedureStart+int(kind)*2+pair])
			provider, err := sqltype.BindCitextPattern(kind, args[0].ResultUsage(), args[1].ResultUsage(), result.Usage(), text.Usage(), base.SQLType.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			n := plan.CitextPatternExpr{Info: expr.Info{Usage: result.Usage(), RoutineReference: p}, Ref: view.Owner().Domain.Ref(view.Epoch(), p.Address()), Args: args, Pattern: provider, Text: text.Usage(), Kind: kind, Namespace: "public", Collation: base.SQLType.Usage().Collation}
			rebuilt, err := n.Rebuild(n.Info, n.Children())
			if err != nil || !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return a == b }, false) {
				t.Fatal(rebuilt, err)
			}
			if _, err := n.Rebuild(n.Info, args[:1]); err == nil {
				t.Fatal("truncated children accepted")
			}
			rendered, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, rebuilt)
			if err != nil || rendered == "" {
				t.Fatal(rendered, err)
			}
			bad := n
			bad.Kind = (kind + 1) % 4
			if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, bad); err == nil {
				t.Fatal("wrong captured kind accepted")
			}
		}
	}
}
