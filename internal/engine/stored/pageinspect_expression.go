package stored

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPageinspect(n plan.InstalledPageinspectExpr, v catalog.Read) error {
	p, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if n.CallRoutineReference() != n.Procedure || extension.PageinspectProviderOf(p) != extension.StockPageinspect {
		return fmt.Errorf("stuntgres: invalid installed pageinspect carrier")
	}
	boundary := n.Pageinspect
	boundary.RoutineReference = p
	return extension.ValidatePageinspectCall(v, p, boundary)
}
func pageinspectCallSQL(n plan.InstalledPageinspectExpr, p *catalog.Procedure, name string, quote func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('(')
	for i, arg := range n.Args {
		if i != 0 {
			b.WriteString(", ")
		}
		slot := int(n.Slots[i])
		label := ""
		if p.InputNames != nil {
			label = p.InputNames.At(slot)
		} else if outputs := p.OutputDeclaration(); outputs != nil {
			label = outputs.Name(slot)
		} else {
			label = p.Installed.ArgumentNames[slot]
		}
		b.WriteString(quote(label))
		b.WriteString(" => ")
		s, err := render(arg)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	b.WriteByte(')')
	return b.String(), nil
}
