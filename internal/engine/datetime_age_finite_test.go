package engine

import (
	"stuntgres/internal/parser"
)

func ageFiniteTyped(typ, value string) parser.Expr {
	return parser.CastExpr{Child: datetimeMakeString(value), TypeName: parser.TypeName{Name: typ}}
}
