package stored

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledPgStatStatements(n plan.InstalledPgStatStatementsExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || !n.InfoOnly && (n.ShowText == nil || n.ShowText.ResultUsage().Type == nil || n.ShowText.ResultUsage().Type.ID() != sqltype.Bool) {
		return fmt.Errorf("stuntgres: invalid statement statistics expression")
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.StockPgStatStatements.ValidateProcedure(v, current); err != nil {
		return err
	}
	slot := 0
	if n.InfoOnly {
		slot = 1
	}
	if current.Origin.Declaration != slot {
		return fmt.Errorf("stuntgres: mismatched statement statistics declaration")
	}
	columns := functions.PgStatStatementsColumns[:]
	if n.InfoOnly {
		columns = functions.PgStatStatementsInfoColumns[:]
	}
	shape := n.Usage.Type
	if shape == nil || !shape.IsComposite() || shape.CompositeLen() != len(columns) {
		return fmt.Errorf("stuntgres: invalid statement statistics OUT shape")
	}
	for i, c := range columns {
		member := shape.CompositeMember(i)
		if member.Attribute != int32(i+1) || member.Name != c.Name || member.Usage.Type == nil || member.Usage.Type.ID() != c.Type {
			return fmt.Errorf("stuntgres: invalid statement statistics OUT shape")
		}
	}
	return nil
}
func pgStatStatementsRecipe(raw parser.Expr) bool {
	call, ok := raw.(parser.FunctionExpr)
	if !ok || len(call.Name.Parts) == 0 {
		return false
	}
	name := call.Name.Parts[len(call.Name.Parts)-1].Text
	return name == "pg_stat_statements" || name == "pg_stat_statements_info" || name == "pg_stat_statements_reset"
}
func (v *View) pgStatStatementsRecipeProcedure(read catalog.Read, path analyze.ViewLiteralPath) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: stale frozen statement statistics reference")
	}
	procedure, ok := read.Procedure(ref.Address)
	if !ok || extension.PgStatStatementsProviderOf(procedure) != extension.StockPgStatStatements {
		return nil, fmt.Errorf("stuntgres: mismatched frozen statement statistics procedure")
	}
	if err := extension.StockPgStatStatements.ValidateProcedure(read, procedure); err != nil {
		return nil, err
	}
	return procedure, nil
}
func (v *QueryRenderer) pgStatStatementsRecipeName(read catalog.Read, path analyze.ViewLiteralPath) (string, error) {
	procedure, err := v.pgStatStatementsRecipeProcedure(read, path)
	if err != nil {
		return "", err
	}
	namespace, ok := read.Namespace(procedure.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen statement statistics namespace")
	}
	return v.Presentation.identifier(namespace.Name()) + "." + v.Presentation.identifier(procedure.Name()), nil
}
