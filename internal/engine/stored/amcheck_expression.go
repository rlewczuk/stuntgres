package stored

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledAmcheck(n plan.InstalledAmcheckExpr, v catalog.Read) error {
	p, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if n.CallRoutineReference() != n.Procedure {
		return fmt.Errorf("stuntgres: invalid installed amcheck carrier")
	}
	boundary := n.Amcheck
	boundary.RoutineReference = p
	return extension.ValidateAmcheckCall(v, p, boundary)
}

// Names preserve written evaluation order even for reordered named calls.
func amcheckCallSQL(n plan.InstalledAmcheckExpr, p *catalog.Procedure, name string, quote func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	var text strings.Builder
	text.WriteString(name)
	text.WriteByte('(')
	for i, arg := range n.Args {
		if i != 0 {
			text.WriteString(", ")
		}
		text.WriteString(quote(p.Installed.ArgumentNames[n.Slots[i]]))
		text.WriteString(" => ")
		sql, err := render(arg)
		if err != nil {
			return "", err
		}
		text.WriteString(sql)
	}
	text.WriteByte(')')
	return text.String(), nil
}
