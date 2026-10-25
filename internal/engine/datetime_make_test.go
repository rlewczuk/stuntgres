package engine

import (
	"stuntgres/internal/parser"
)

func datetimeMakeRaw(name string, values ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: values, Pos: parser.Position{Character: 8}}
}

func datetimeMakeInteger(n int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
}

func datetimeMakeString(s string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
}
