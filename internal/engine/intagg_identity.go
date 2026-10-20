package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func ValidateIntaggIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef, name string) error {
	if proc == nil {
		if ref != (ids.ObjectRef{}) {
			return fmt.Errorf("stuntgres: missing installed intagg identity")
		}
		return nil
	}
	actual, ok := v.Procedure(ref.Address)
	if !ok || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || proc.Name() != name || IntaggProviderOf(proc) != StockIntagg {
		return fmt.Errorf("stuntgres: invalid stored intagg identity")
	}
	return StockIntagg.ValidateProcedure(v, proc)
}

func IntaggRecipeFunction(raw parser.Expr) string {
	switch call := raw.(type) {
	case parser.AggregateExpr:
		if call.Name == "int_array_aggregate" {
			return call.Name
		}
	case parser.FunctionExpr:
		if len(call.Name.Parts) != 0 && call.Name.Parts[len(call.Name.Parts)-1].Text == "int_array_enum" {
			return "int_array_enum"
		}
	}
	return ""
}
