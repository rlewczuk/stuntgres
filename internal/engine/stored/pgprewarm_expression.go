package stored

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgPrewarm(n plan.InstalledPgPrewarmExpr, v catalog.Read) error {
	if !ReferenceEpochValid(v, nil, n.Ref) { return fmt.Errorf("stuntgres: invalid installed pg_prewarm reference") }
	proc, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil { return err }
	if n.CallRoutineReference() != n.Procedure || extension.PgPrewarmProviderOf(proc) != extension.StockPgPrewarm { return fmt.Errorf("stuntgres: invalid installed pg_prewarm carrier") }
	boundary := n.PgPrewarm
	boundary.RoutineReference = proc
	return extension.ValidatePgPrewarmCall(v, proc, boundary)
}
func pgPrewarmCallSQL(n plan.InstalledPgPrewarmExpr, proc *catalog.Procedure, name string, quote func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	var out strings.Builder
	out.WriteString(name)
	out.WriteByte('(')
	for i, arg := range n.Args {
		if i != 0 { out.WriteString(", ") }
		if label := proc.InputNames.At(int(n.Slots[i])); label != "" { out.WriteString(quote(label)); out.WriteString(" => ") }
		text, err := render(arg)
		if err != nil { return "", err }
		out.WriteString(text)
	}
	out.WriteByte(')')
	return out.String(), nil
}
