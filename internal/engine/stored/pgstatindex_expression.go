package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgstatindex(n plan.InstalledPgstatindexExpr, v catalog.Read) error {
	p, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if n.CallRoutineReference() != n.Procedure || extension.PgstattupleProviderOf(p) != extension.StockPgstattuple {
		return fmt.Errorf("stuntgres: invalid installed pgstatindex carrier")
	}
	boundary := n.Pgstatindex
	boundary.RoutineReference = p
	return extension.ValidatePgstatindexCall(v, p, boundary)
}
func pgstatindexCallSQL(n plan.InstalledPgstatindexExpr, p *catalog.Procedure, name string, quote func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	arg, err := render(n.Args[0])
	if err != nil {
		return "", err
	}
	return name + "(" + quote(p.OutputDeclaration().Name(0)) + " => " + arg + ")", nil
}
