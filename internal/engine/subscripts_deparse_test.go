package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSubscriptsDeparse(t *testing.T) {
	view := New().CatalogRead()
	e := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: -2, Length: 3}}, 1)
	arrayLiteral := e.Args[0].(literalExpression)
	integerLiteral := e.Args[1].(literalExpression)
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
			definition := &viewDefinition{literals: make(map[viewLiteralPath]literalExpression), castUsages: make(map[viewLiteralPath]sqltype.Usage)}
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
				definition.castUsages[viewLiteralPath{node: i + 1}] = usage
			}
			definition.literals[viewLiteralPath{node: tc.casts + 1}] = arrayLiteral
			definition.literals[viewLiteralPath{node: tc.casts + 2}] = integerLiteral
			call := nullCountRaw("generate_subscripts", raw, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}})
			renderer := &viewRenderer{viewDefinition: definition}
			ordinal := 0
			text, err := renderer.expressionSQLAt(context.Background(), view, call, viewLiteralPath{}, &ordinal, pgRenderParent{})
			if err != nil || text != tc.want || ordinal != tc.casts+3 || strings.Count(text, "CAST(") != tc.casts {
				t.Fatal("typed leaf/source cast ownership or ordinal changed", text, tc.want, ordinal, err)
			}
		})
	}
}
