package regobj

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestGeometryConversionCBoxLiteralTransport(t *testing.T) {
	var box *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Box {
			box = d
		}
	}
	ctx := context.Background()
	value, err := box.Input(ctx, "(1,1),(0,0)", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value sqltype.Value
		want  string
	}{
		{value, "CAST('(1,1),(0,0)' AS BOX)"},
		{sqltype.Null(box), "CAST(NULL AS BOX)"},
	} {
		literal := plan.LiteralExpr{Info: expr.Info{Usage: box.Usage()}, Value: tc.value}
		got, err := ConstantLiteral(ctx, literal)
		if err != nil || got != tc.want {
			t.Fatal("BOX transport lost formal identity", got, err)
		}
	}
}
