package engine

import (
	"stuntgres/internal/parser"
)

func arrayInspectUnitRaw(name string, args ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
}

func arrayInspectUnitArray(input string) parser.Expr {
	return parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}, TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{-1}}}
}
