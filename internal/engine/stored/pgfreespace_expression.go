package stored

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgFreespace(n plan.InstalledPgFreespaceExpr, v catalog.Read) error {
	if !ReferenceEpochValid(v, nil, n.Ref) {
		return fmt.Errorf("stuntgres: invalid installed pg_freespace reference")
	}
	proc, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if n.CallRoutineReference() != n.Procedure || extension.PgFreespaceProviderOf(proc) != extension.StockPgFreespace {
		return fmt.Errorf("stuntgres: invalid installed pg_freespace carrier")
	}
	boundary := n.PgFreespace
	boundary.RoutineReference = proc
	return extension.ValidatePgFreespaceCall(v, proc, boundary)
}
func pgFreespaceCallSQL(n plan.InstalledPgFreespaceExpr, proc *catalog.Procedure, name string, quote func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	var out strings.Builder
	out.WriteString(name)
	out.WriteByte('(')
	for i, arg := range n.Args {
		if i != 0 {
			out.WriteString(", ")
		}
		label := ""
		if n.Kind.Set() {
			label = proc.OutputDeclaration().Name(int(n.Slots[i]))
		}
		if label != "" {
			out.WriteString(quote(label))
			out.WriteString(" => ")
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
