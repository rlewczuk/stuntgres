package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// IntaggEnumeration is intagg's bound int_array_enum call before the engine
// attaches its installed procedure. Attach applies set-returning admission
// and returns the engine's set-returning node, so this node is never
// evaluated.
type IntaggEnumeration struct {
	expr.Info
	Arg expr.Expr
}

func (IntaggEnumeration) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: unattached int_array_enum call")
}
