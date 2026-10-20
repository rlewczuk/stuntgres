package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func sameIntaggIdentity(a *catalog.Procedure, ar ids.ObjectRef, b *catalog.Procedure, br ids.ObjectRef) bool {
	if ar != br || (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Address() == b.Address() && a.Revision() == b.Revision()
}

func validateIntaggIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef, name string) error {
	if proc == nil {
		if ref != (ids.ObjectRef{}) {
			return fmt.Errorf("stuntgres: missing installed intagg identity")
		}
		return nil
	}
	actual, ok := v.Procedure(ref.Address)
	if !ok || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || proc.Name() != name || intaggProviderOf(proc) != stockIntagg {
		return fmt.Errorf("stuntgres: invalid stored intagg identity")
	}
	return stockIntagg.validateProcedure(v, proc)
}

func intaggRecipeFunction(raw parser.Expr) string {
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
