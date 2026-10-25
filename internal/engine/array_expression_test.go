package engine

import (
	"stuntgres/internal/parser"
)

func arrayUnitInteger(n int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
}
