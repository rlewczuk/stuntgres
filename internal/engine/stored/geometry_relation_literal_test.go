package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exercise the actual captured-source serializer, which ordinary view dumps
// use instead of the analyzed provider's Render method. Public controls retain
// their byte-for-byte Dump/Restore/redump assertions.
func TestGeometryRelationLiteralRecipe(t *testing.T) {
	read, _, _, _ := routineCaptureFixture(t)
	ctx := context.Background()
	path := analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}
	for _, tc := range []struct {
		id    sqltype.ID
		input string
	}{
		{sqltype.Point, "(1,1)"}, {sqltype.Line, "{0,1,0}"},
		{sqltype.Lseg, "[(0,0),(2,0)]"}, {sqltype.Polygon, "((0,0),(2,0),(0,2))"},
		{sqltype.Box, "(2,2),(0,0)"}, {sqltype.Circle, "<(0,0),1>"},
		{sqltype.Path, "[(0,0),(2,0)]"},
	} {
		typ, ok := read.LookupType(tc.id)
		if !ok {
			t.Fatal("missing geometry descriptor", tc.id)
		}
		value, err := typ.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []sqltype.Value{value, sqltype.Null(typ)} {
			literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
			want, err := regobj.ConstantLiteral(ctx, literal)
			if err != nil {
				t.Fatal(err)
			}
			// Zero/one source cast render identically. An intentional outer cast
			// remains; only the matching captured leaf annotation is elided.
			for depth := range 3 {
				var raw parser.Expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue}}
				casts := make(map[analyze.ViewLiteralPath]sqltype.Usage)
				for i := range depth {
					raw = parser.CastExpr{Child: raw}
					key := path
					key.Node = i
					casts[key] = typ.Usage()
				}
				leaf := path
				leaf.Node = depth
				view := NewView(ViewParts{Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{leaf: literal}, CastUsages: casts})
				renderer := NewQueryRenderer(view)
				ordinal := 0
				got, err := renderer.ExpressionSQLAt(ctx, read, raw, path, &ordinal, DeparseParent{})
				expected := want
				if depth == 2 {
					expected = "CAST(" + want + " AS " + typ.SQLName() + ")"
				}
				if err != nil || got != expected {
					t.Fatal("geometric leaf cast accumulation/outer cast loss", tc.id, depth, got, expected, err)
				}
			}
			// A real different-Usage IO conversion must survive unchanged.
			text, _ := read.LookupType(sqltype.Text)
			leaf := path
			leaf.Node = 1
			view := NewView(ViewParts{Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{leaf: literal}, CastUsages: map[analyze.ViewLiteralPath]sqltype.Usage{path: text.Usage()}})
			raw := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue}}}
			ordinal := 0
			got, err := NewQueryRenderer(view).ExpressionSQLAt(ctx, read, raw, path, &ordinal, DeparseParent{})
			expected := "CAST(" + want + " AS TEXT)"
			if err != nil || got != expected {
				t.Fatal("different-Usage conversion lost", tc.id, got, expected, err)
			}
		}
	}
}
