package enginetests_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSubscriptsDeparse(t *testing.T) {
	view := engine.New().CatalogRead()
	e := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: -2, Length: 3}}, 1)
	arrayLiteral := e.Args[0].(plan.LiteralExpr)
	integerLiteral := e.Args[1].(plan.LiteralExpr)
	arrayUsage := arrayLiteral.ResultUsage()
	wide, _ := view.LookupType(sqltype.Int8)
	wideArray, _ := view.LookupArray(wide)
	for _, tc := range []struct {
		name  string
		casts int
		outer sqltype.Usage
		want  string
	}{
		{"owning-cast", 1, arrayUsage, "pg_catalog.generate_subscripts(CAST('[-2:0]={NULL,NULL,NULL}' AS INTEGER[]), 1)"},
		{"nested-identity", 2, arrayUsage, "pg_catalog.generate_subscripts(CAST(CAST('[-2:0]={NULL,NULL,NULL}' AS INTEGER[]) AS INTEGER[]), 1)"},
		{"distinct-target", 2, wideArray.Usage(), "pg_catalog.generate_subscripts(CAST(CAST('[-2:0]={NULL,NULL,NULL}' AS INTEGER[]) AS BIGINT[]), 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw parser.Expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "[-2:0]={NULL,NULL,NULL}"}}
			definition := stored.NewView(stored.ViewParts{Literals: make(map[analyze.ViewLiteralPath]plan.LiteralExpr), CastUsages: make(map[analyze.ViewLiteralPath]sqltype.Usage)})
			for i := range tc.casts {
				usage := arrayUsage
				if i == 0 {
					usage = tc.outer
				}
				name := "integer"
				if i == tc.casts-1 && tc.outer != arrayUsage {
					name = "bigint"
				}
				raw = parser.CastExpr{Child: raw, TypeName: parser.TypeName{Name: name, ArrayBounds: []int32{0}}}
				definition.CastUsages()[analyze.ViewLiteralPath{Node: i + 1}] = usage
			}
			definition.Literals()[analyze.ViewLiteralPath{Node: tc.casts + 1}] = arrayLiteral
			definition.Literals()[analyze.ViewLiteralPath{Node: tc.casts + 2}] = integerLiteral
			call := nullCountRaw("generate_subscripts", raw, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}})
			renderer := &stored.QueryRenderer{View: definition}
			ordinal := 0
			text, err := renderer.ExpressionSQLAt(context.Background(), view, call, analyze.ViewLiteralPath{}, &ordinal, stored.DeparseParent{})
			if err != nil || text != tc.want || ordinal != tc.casts+3 || strings.Count(text, "CAST(") != tc.casts {
				t.Fatal("typed leaf/source cast ownership or ordinal changed", text, tc.want, ordinal, err)
			}
		})
	}
}

func subscriptsUnit(t *testing.T, view catalog.Read, dimensions []sqltype.ArrayDimension, dimension int64, reverse ...bool) plan.SubscriptsExpr {
	t.Helper()
	d, _ := view.LookupType(sqltype.Int4)
	array, ok := view.LookupArray(d)
	if !ok {
		t.Fatal("integer array")
	}
	count := 0
	if len(dimensions) != 0 {
		count = 1
		for _, dim := range dimensions {
			count *= int(dim.Length)
		}
	}
	elements := make([]sqltype.Value, count)
	for i := range elements {
		elements[i] = sqltype.Null(d)
	}
	value, err := sqltype.ArrayValue(array, dimensions, elements)
	if err != nil {
		t.Fatal(err)
	}
	integer, err := sqltype.Integer(d, dimension)
	if err != nil {
		t.Fatal(err)
	}
	e := plan.SubscriptsExpr{Info: expr.Info{Usage: d.Usage(), Pos: parser.Position{Character: 8, Offset: 7}, Property: expr.Properties{StageDependent: true}}, Args: []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: value},
		plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: integer},
	}}
	if len(reverse) != 0 {
		boolean, _ := view.LookupType(sqltype.Bool)
		e.Args = append(e.Args, plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, reverse[0])})
	}
	return e
}

func nullCountRaw(name string, args ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args, Pos: parser.Position{Character: 8}}
}
