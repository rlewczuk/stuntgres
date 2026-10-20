package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) FunctionArrayRandomOwner() (expr.RandomOwner, error) {
	scope := h.scope()
	if scope.op == nil || scope.op.Session == nil {
		return nil, fmt.Errorf("stuntgres: missing array random session scope")
	}
	return &scope.op.Session.ArrayRandom, nil
}

func (h *statementHost) FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error) {
	procedure, err := h.scope().conversionProcedure(source, destination)
	if err != nil || procedure == nil {
		return nil, err
	}
	return &procedure.Conversion, nil
}
