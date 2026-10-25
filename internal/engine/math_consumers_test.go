package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type mathObservedArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (e mathObservedArgument) Eval(expr.Context) (sqltype.Value, error) {
	*e.order = append(*e.order, e.index)
	return e.value, e.err
}
