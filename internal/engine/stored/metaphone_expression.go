package stored

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
	proc, symbol, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil || extension.FuzzystrmatchProviderOf(proc) != extension.StockFuzzystrmatch || symbol != n.Function || proc.Signature != catalog.ProcedureSignature(functions.MetaphoneSignature(n.Function, len(n.Args))) || proc.Result != 25 {
		return fmt.Errorf("stuntgres: invalid stored Metaphone identity")
	}
	if err := extension.StockFuzzystrmatch.ValidateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
