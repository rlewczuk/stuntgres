package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPGLSNLiteralRecipe(t *testing.T) {
	read, _, _, _ := routineCaptureFixture(t)
	ctx := context.Background()
	typ, ok := read.LookupType(sqltype.PgLSN)
	if !ok {
		t.Fatal("missing pg_lsn descriptor")
	}
	text, _ := read.LookupType(sqltype.Text)
	path := analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}
	for _, input := range []string{"0/0", "80000000/0", "FFFFFFFF/FFFFFFFF"} {
		value, err := typ.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []sqltype.Value{value, sqltype.Null(typ)} {
			literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
			want := "'" + input + "'::pg_lsn"
			if value.IsNull() {
				want = "NULL::pg_lsn"
			}
			for _, leaf := range []parser.Expr{
				parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue}},
				parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}},
				parser.TypedLiteralExpr{},
			} {
				// The captured leaf owns one exact annotation. Written outer
				// casts and conversions to another Usage must remain intact.
				for depth := range 3 {
					var raw parser.Expr = leaf
					casts := make(map[analyze.ViewLiteralPath]sqltype.Usage)
					for i := range depth {
						raw = parser.CastExpr{Child: raw}
						key := path
						key.Node = i
						casts[key] = typ.Usage()
					}
					key := path
					key.Node = depth
					view := NewView(ViewParts{Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{key: literal}, CastUsages: casts})
					ordinal := 0
					got, err := NewQueryRenderer(view).ExpressionSQLAt(ctx, read, raw, path, &ordinal, DeparseParent{})
					expected := want
					if depth == 2 {
						expected = "CAST(" + want + " AS PG_LSN)"
					}
					if err != nil || got != expected {
						t.Fatal("pg_lsn leaf cast accumulation/outer cast loss", input, depth, got, expected, err)
					}
				}
				key := path
				key.Node = 1
				view := NewView(ViewParts{Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{key: literal}, CastUsages: map[analyze.ViewLiteralPath]sqltype.Usage{path: text.Usage()}})
				ordinal := 0
				got, err := NewQueryRenderer(view).ExpressionSQLAt(ctx, read, parser.CastExpr{Child: leaf}, path, &ordinal, DeparseParent{})
				expected := "CAST(" + want + " AS TEXT)"
				if err != nil || got != expected {
					t.Fatal("different-Usage conversion lost", input, got, expected, err)
				}
			}
		}
	}
}
