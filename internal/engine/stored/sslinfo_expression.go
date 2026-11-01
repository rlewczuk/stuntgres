package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledSSLInfo(n plan.InstalledSSLInfoExpr, v catalog.Read) error {
	proc, symbol, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil || symbol != n.Name || !analyze.ValidInstalledSSLInfoShape(n) {
		return fmt.Errorf("stuntgres: invalid stored sslinfo identity")
	}
	result, ok := v.LookupType(n.Usage.Type.ID())
	if !ok || result != n.Usage.Type {
		return fmt.Errorf("stuntgres: invalid stored sslinfo result generation")
	}
	return extension.StockSSLInfo.ValidateProcedure(v, proc)
}
