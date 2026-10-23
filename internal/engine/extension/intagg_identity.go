package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func ValidateIntaggIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef, name string) error {
	if proc == nil {
		if ref != (ids.ObjectRef{}) {
			return fmt.Errorf("stuntgres: missing installed intagg identity")
		}
		return nil
	}
	actual, ok := v.Procedure(ref.Address)
	symbol, err := ProcedureSymbol(v, proc)
	if !ok || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || err != nil || symbol != name || IntaggProviderOf(proc) != StockIntagg {
		return fmt.Errorf("stuntgres: invalid stored intagg identity")
	}
	return StockIntagg.ValidateProcedure(v, proc)
}
