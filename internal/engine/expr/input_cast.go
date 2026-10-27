package expr

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// InputCast exposes a validated logical cast and its real executable source.
// The transport may have another concrete Usage; no UNKNOWN datum is implied.
type InputCast struct {
	Source      Expr
	Target      sqltype.Usage
	Syntax      parser.CastExpr
	Transparent []TransparentInputCast
}

type InputCaster interface {
	LogicalInputCast() (InputCast, error)
}
