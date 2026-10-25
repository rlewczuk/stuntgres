package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func moreUnitExpression(t *testing.T, view catalog.Read) plan.UnnestExpr {
	t.Helper()
	m, _ := view.LookupType(sqltype.Int4Multirange)
	r, _ := view.LookupType(sqltype.Int4Range)
	v, err := m.Input(context.Background(), "{[8,9),[1,3)}", -1)
	if err != nil {
		t.Fatal(err)
	}
	return plan.UnnestExpr{
		Info: expr.Info{Usage: r.Usage(), Pos: parser.Position{Offset: 7, Character: 8}, Property: expr.Properties{Volatility: expr.Immutable, StageDependent: true}},
		Args: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: m.Usage()}, Value: v}},
	}
}
