package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledMetaphone(n plan.InstalledMetaphoneExpr, v catalog.Read) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored Metaphone argument")
		}
	}
	proc, ok := v.Procedure(n.Ref.Address)
	if !ok || proc != n.Procedure || extension.FuzzystrmatchProviderOf(proc) != extension.StockFuzzystrmatch || proc.Name() != n.Function || proc.Signature != catalog.ProcedureSignature(functions.MetaphoneSignature(n.Function, len(n.Args))) || proc.Result != 25 {
		return fmt.Errorf("stuntgres: invalid stored Metaphone identity")
	}
	if err := extension.StockFuzzystrmatch.ValidateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
