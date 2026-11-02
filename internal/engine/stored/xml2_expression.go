package stored

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledXML2(n plan.InstalledXML2Expr, v catalog.Read) error {
	proc, symbol, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil || symbol != n.Name || !analyze.ValidInstalledXML2Shape(n) {
		return fmt.Errorf("stuntgres: invalid stored xml2 identity")
	}
	result, ok := v.LookupType(n.Usage.Type.ID())
	if !ok || result != n.Usage.Type {
		return fmt.Errorf("stuntgres: invalid stored xml2 result generation")
	}
	for _, arg := range n.Args {
		typ, ok := v.LookupType(arg.ResultUsage().Type.ID())
		if !ok || typ != arg.ResultUsage().Type {
			return fmt.Errorf("stuntgres: invalid stored xml2 operand generation")
		}
	}
	return extension.StockXML2.ValidateProcedure(v, proc)
}
