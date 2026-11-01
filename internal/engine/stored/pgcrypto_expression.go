package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledCrypto(n plan.InstalledCryptoExpr, v catalog.Read) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored crypto argument")
		}
	}
	proc, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	name, nameErr := extension.ProcedureInstallationName(v, proc)
	if err != nil || nameErr != nil || extension.PgcryptoProviderOf(proc) != extension.StockPgcrypto || name != n.Plan.Name() || proc.Signature != analyze.CryptoExpressionSignature(n) {
		return fmt.Errorf("stuntgres: invalid stored crypto identity")
	}
	if err := extension.StockPgcrypto.ValidateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
