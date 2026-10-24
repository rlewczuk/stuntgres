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

func TestCitextComparisonStoredCapture(t *testing.T) {
	e, ext, base := citextUnitInstalled(t)
	view := e.CatalogRead()
	result, _ := view.LookupType(sqltype.Bool)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: base.SQLType.Usage()}, Value: sqltype.String(base.SQLType, "ABC")}
	for kind := range extension.CitextComparisonNames {
		p := view.MustProcedure(ext.Members[3+kind])
		comparison, err := sqltype.BindCitextComparison(sqltype.ComparisonOperator(kind), base.SQLType, base.SQLType, result, base.SQLType.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		n := plan.CitextComparisonExpr{Info: expr.Info{Usage: result.Usage(), RoutineReference: p}, Ref: view.Owner().Domain.Ref(view.Epoch(), p.Address()), Args: [2]expr.Expr{literal, literal}, Comparison: comparison, Kind: uint8(kind), Namespace: "public", Collation: base.SQLType.Usage().Collation}
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
		if n.RoutineReference != p || p.Signature != extension.CitextComparisonSignature(base.SQLType.ID()) {
			t.Fatal("source capture mutated")
		}
	}
}
