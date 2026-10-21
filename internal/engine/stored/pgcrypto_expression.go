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
	proc, ok := v.Procedure(n.Ref.Address)
	if !ok || proc != n.Procedure || extension.PgcryptoProviderOf(proc) != extension.StockPgcrypto || proc.Name() != n.Plan.Name() || proc.Signature != analyze.CryptoExpressionSignature(n) {
		return fmt.Errorf("stuntgres: invalid stored crypto identity")
	}
	if err := extension.StockPgcrypto.ValidateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
