package engine

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func seriesUnit(t *testing.T, d *sqltype.Descriptor, values ...int64) plan.SeriesExpr {
	t.Helper()
	e := plan.SeriesExpr{Info: expr.Info{Usage: d.Usage(), Pos: parser.Position{Character: 8, Offset: 7}, Property: expr.Properties{StageDependent: true}}}
	for _, n := range values {
		v, err := sqltype.Integer(d, n)
		if err != nil {
			t.Fatal(err)
		}
		e.Args = append(e.Args, plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: v})
	}
	return e
}

func seriesUnitType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == id {
			return d
		}
	}
	t.Fatalf("missing builtin descriptor %d", id)
	return nil
}
