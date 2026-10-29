package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledAutoinc(n plan.InstalledAutoincExpr, v catalog.Read) error {
	if err:=n.ValidateStoredProvider();err!=nil { return err }
	p,symbol,err:=currentInstalledProcedure(v,n.Ref,n.Procedure)
	if err!=nil || symbol!="autoinc" || extension.AutoincProviderOf(p)!=extension.StockAutoinc {
		return fmt.Errorf("stuntgres: invalid stored autoinc identity")
	}
	return extension.StockAutoinc.ValidateProcedure(v,p)
}
