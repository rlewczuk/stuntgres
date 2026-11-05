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
	if err != nil || n.Ref.Epoch != v.Epoch() || symbol != n.Name || !analyze.ValidInstalledSSLInfoShape(n) {
		return fmt.Errorf("stuntgres: invalid stored sslinfo identity")
	}
	if n.Procedure.Installed.ReturnsSet {
		for i := range n.Usage.Type.CompositeLen() {
			field := n.Usage.Type.CompositeMember(i)
			current, ok := v.LookupType(field.Usage.Type.ID())
			if !ok || field.Usage != current.Usage() {
				return fmt.Errorf("stuntgres: invalid stored sslinfo OUT generation")
			}
		}
	} else {
		result, ok := v.LookupType(n.Usage.Type.ID())
		if !ok || n.Usage != result.Usage() {
			return fmt.Errorf("stuntgres: invalid stored sslinfo result generation")
		}
	}
	if n.Arg != nil {
		current, ok := v.LookupType(n.Arg.ResultUsage().Type.ID())
		if !ok || n.Arg.ResultUsage() != current.Usage() {
			return fmt.Errorf("stuntgres: invalid stored sslinfo argument generation")
		}
	}
	return extension.StockSSLInfo.ValidateProcedure(v, proc)
}
