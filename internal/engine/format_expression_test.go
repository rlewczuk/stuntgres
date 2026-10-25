package engine

import (
	"stuntgres/internal/parser"
)

func formatUnitLiteral(text string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
}
