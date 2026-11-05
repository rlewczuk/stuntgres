package stored

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledPgStatStatementsReset(n plan.InstalledPgStatStatementsResetExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || n.Ref.Epoch == 0 || n.Ref.Epoch > v.Epoch() {
		return fmt.Errorf("stuntgres: invalid statement statistics reset expression")
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.StockPgStatStatements.ValidateProcedure(v, current); err != nil {
		return err
	}
	result, ok := v.LookupType(sqltype.Timestamptz)
	if !ok || current.Origin.Declaration != 2 || n.Usage.Type != result {
		return fmt.Errorf("stuntgres: mismatched statement statistics reset declaration")
	}
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing statement statistics reset operand")
		}
		typ := arg.ResultUsage().Type
		currentType, exists := v.LookupType(typ.ID())
		if !exists || typ != currentType {
			return fmt.Errorf("stuntgres: foreign statement statistics reset operand type")
		}
	}
	if _, err := n.Rebuild(n.Info, n.Args[:]); err != nil {
		return err
	}
	// Omitted operands belong to the sealed literal-default recipe. Rendering
	// may omit them only while the executable children still hold those values.
	for i := int(n.WrittenCount); i < len(n.Args); i++ {
		slot := n.Order[i]
		literal, ok := n.Args[slot].(plan.LiteralExpr)
		if !ok || literal.Value.IsNull() || literal.Value.Type() != literal.ResultUsage().Type {
			return fmt.Errorf("stuntgres: invalid statement statistics reset default")
		}
		valid := false
		switch slot {
		case 0, 1:
			value, err := literal.Value.OID()
			valid = err == nil && value == 0
		case 2:
			value, err := literal.Value.Integer()
			valid = err == nil && value == 0
		case 3:
			value, err := literal.Value.Boolean()
			valid = err == nil && !value
		}
		if !valid {
			return fmt.Errorf("stuntgres: altered statement statistics reset default")
		}
	}
	return nil
}

// Render only written operands, preserving their order and named notation.
// Omitted defaults remain the genuine procedure's default recipe.
func pgStatStatementsResetCallSQL(n plan.InstalledPgStatStatementsResetExpr, proc *catalog.Procedure, name string, identifier func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	var args [4]string
	for i := range n.WrittenCount {
		slot := n.Order[i]
		value, err := render(n.Args[slot])
		if err != nil {
			return "", err
		}
		args[i] = value
		if n.Labels[i] != "" {
			args[i] = identifier(proc.Installed.ArgumentNames[slot]) + " => " + value
		}
	}
	return name + "(" + strings.Join(args[:n.WrittenCount], ", ") + ")", nil
}
