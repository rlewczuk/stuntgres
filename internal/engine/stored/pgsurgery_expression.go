package stored

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgsurgery(n plan.InstalledPgsurgeryExpr, v catalog.Read) error {
	if !ReferenceEpochValid(v, nil, n.Ref) {
		return fmt.Errorf("stuntgres: invalid installed pg_surgery reference")
	}
	proc, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if n.CallRoutineReference() != n.Procedure || extension.PgsurgeryProviderOf(proc) != extension.StockPgsurgery {
		return fmt.Errorf("stuntgres: invalid installed pg_surgery carrier")
	}
	boundary := n.Pgsurgery
	boundary.RoutineReference = proc
	return extension.ValidatePgsurgeryCall(v, proc, boundary)
}
func pgsurgeryCallSQL(n plan.InstalledPgsurgeryExpr, proc *catalog.Procedure, name string, quote func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	var out strings.Builder
	out.WriteString(name)
	out.WriteByte('(')
	for i, arg := range n.Args {
		if i != 0 {
			out.WriteString(", ")
		}
		out.WriteString(quote(proc.Installed.ArgumentNames[n.Slots[i]]))
		out.WriteString(" => ")
		text, err := render(arg)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}
	out.WriteByte(')')
	return out.String(), nil
}
