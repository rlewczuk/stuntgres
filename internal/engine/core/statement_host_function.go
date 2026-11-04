package core

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) FunctionArrayRandomOwner() (expr.RandomOwner, error) {
	scope := h.scope()
	if scope.Op == nil || scope.Op.Session == nil {
		return nil, fmt.Errorf("stuntgres: missing array random session scope")
	}
	return &scope.Op.Session.ArrayRandom, nil
}

func (h *statementHost) FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error) {
	selected, err := h.scope().conversionSelection(source, destination)
	if err != nil || selected.procedure == nil {
		return nil, err
	}
	handler, err := selectedConversionHandler(h.scope().Inquiry.visibilityCatalog, selected)
	if err != nil {
		return nil, err
	}
	return &handler, nil
}
