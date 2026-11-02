package stored

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledDblinkBuildSQLUpdate(n plan.InstalledDblinkBuildSQLUpdateExpr, view catalog.Read) error {
	if !ReferenceEpochValid(view, nil, n.Ref) {
		return fmt.Errorf("stuntgres: invalid installed dblink reference")
	}
	p, _, err := currentInstalledProcedure(view, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if n.CallRoutineReference() != n.Procedure || extension.DblinkProviderOf(p) != extension.StockDblink {
		return fmt.Errorf("stuntgres: invalid installed dblink carrier")
	}
	boundary := n.DblinkBuildSQLUpdate
	boundary.RoutineReference = p
	return extension.ValidateDblinkBuildSQLUpdateCall(view, p, boundary)
}

func dblinkUpdateCallSQL(n plan.InstalledDblinkBuildSQLUpdateExpr, name string, render func(expr.Expr) (string, error)) (string, error) {
	var out strings.Builder
	out.WriteString(name)
	out.WriteByte('(')
	for i, arg := range n.Args {
		if i != 0 {
			out.WriteString(", ")
		}
		text, err := render(arg)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}
	out.WriteByte(')')
	return out.String(), nil
}
