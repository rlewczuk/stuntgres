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

func TestGeometryMeasurePathLiteralTransport(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		id    sqltype.ID
		input string
		want  string
		null  string
	}{
		{sqltype.Path, "[(0,0),(3,4)]", "CAST('[(0,0),(3,4)]' AS PATH)", "CAST(NULL AS PATH)"},
		{sqltype.Lseg, "[(0,0),(3,4)]", "CAST('[(0,0),(3,4)]' AS LSEG)", "CAST(NULL AS LSEG)"},
		{sqltype.Polygon, "((0,0))", "CAST('((0,0))' AS POLYGON)", "CAST(NULL AS POLYGON)"},
	} {
		var descriptor *sqltype.Descriptor
		for _, d := range sqltype.Builtins() {
			if d.ID() == tc.id {
				descriptor = d
			}
		}
		t.Run(descriptor.SQLName(), func(t *testing.T) {
			value, err := descriptor.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, literalCase := range []struct {
				value sqltype.Value
				want  string
			}{
				{value, tc.want},
				{sqltype.Null(descriptor), tc.null},
			} {
				literal := plan.LiteralExpr{Info: expr.Info{Usage: descriptor.Usage()}, Value: literalCase.value}
				got, err := ConstantLiteral(ctx, literal)
				if err != nil || got != literalCase.want {
					t.Fatal("measurement transport lost formal identity", got, err)
				}
			}
		})
	}
}

func TestGeometryRelationLiteralTransport(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	ctx := context.Background()
	for _, tc := range []struct {
		id                sqltype.ID
		input, want, null string
	}{
		{sqltype.Point, "(1,1)", "CAST('(1,1)' AS POINT)", "CAST(NULL AS POINT)"},
		{sqltype.Line, "{0,1,0}", "CAST('{0,1,0}' AS LINE)", "CAST(NULL AS LINE)"},
	} {
		d := types[tc.id]
		value, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, sample := range []struct {
			value sqltype.Value
			want  string
		}{
			{value, tc.want}, {sqltype.Null(d), tc.null},
		} {
			literal := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: sample.value}
			got, err := ConstantLiteral(ctx, literal)
			if err != nil || got != sample.want {
				t.Fatal("captured geometric formal identity", tc.id, got, sample.want, err)
			}
		}
	}
}
