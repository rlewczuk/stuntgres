package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func currentInstalledProcedure(v catalog.Read, ref ids.ObjectRef, captured *catalog.Procedure) (*catalog.Procedure, string, error) {
	if captured == nil || ref.Address != captured.Address() || ref.Address.SubID != 0 || v.Owner() == nil || ref.Domain() != v.Owner().Domain {
		return nil, "", fmt.Errorf("stuntgres: invalid installed routine capture")
	}
	current, err := exec.ValidateStoredRoutineDescriptor(v, captured)
	if err != nil {
		return nil, "", err
	}
	symbol, err := extension.ProcedureSymbol(v, current)
	if err != nil {
		return nil, "", err
	}
	return current, symbol, nil
}
