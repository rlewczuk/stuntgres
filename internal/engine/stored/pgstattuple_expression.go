package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgstattuple(n plan.InstalledPgstattupleExpr, v catalog.Read) error {
	p, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if n.CallRoutineReference() != n.Procedure || extension.PgstattupleProviderOf(p) != extension.StockPgstattuple {
		return fmt.Errorf("stuntgres: invalid installed pgstattuple carrier")
	}
	boundary := n.Pgstattuple
	boundary.RoutineReference = p
	return extension.ValidatePgstattupleCall(v, p, boundary)
}

func pgstattupleCallSQL(n plan.InstalledPgstattupleExpr, p *catalog.Procedure, name string, quote func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	arg, err := render(n.Args[0])
	if err != nil {
		return "", err
	}
	formal := p.Installed.ArgumentNames[0]
	if outputs := p.OutputDeclaration(); outputs != nil {
		formal = outputs.Name(0)
	}
	return name + "(" + quote(formal) + " => " + arg + ")", nil
}
