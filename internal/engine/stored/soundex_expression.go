package stored

import (
	"fmt"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func ValidateInstalledSoundex(n plan.InstalledSoundexExpr, v catalog.Read) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored Soundex argument")
		}
	}
	result := ids.OID(25)
	if n.Function == "difference" {
		result = 23
	}
	proc, ok := v.Procedure(n.Ref.Address)
	if !ok || proc != n.Procedure || extension.FuzzystrmatchProviderOf(proc) != extension.StockFuzzystrmatch || proc.Name() != n.Function || proc.Signature != catalog.ProcedureSignature(functions.SoundexSignature(n.Function, len(n.Args))) || proc.Result != result {
		return fmt.Errorf("stuntgres: invalid stored Soundex identity")
	}
	if err := extension.StockFuzzystrmatch.ValidateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
