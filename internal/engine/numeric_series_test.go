package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func numericSeriesUnit(t *testing.T, d *sqltype.Descriptor, args ...string) plan.SeriesExpr {
	t.Helper()
	e := plan.SeriesExpr{Info: expr.Info{Usage: d.Usage(), Pos: parser.Position{Character: 8, Offset: 7}, Property: expr.Properties{StageDependent: true}}}
	for _, s := range args {
		v, err := d.Input(context.Background(), s, -1)
		if err != nil {
			t.Fatal(err)
		}
		e.Args = append(e.Args, plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: v})
	}
	return e
}
