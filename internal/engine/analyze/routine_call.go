package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

// RoutineCallSchema exposes the checked invocation tuple with declaration-owned
// labels and order, never body aliases, pseudo-types or a synthetic VOID column.
func RoutineCallSchema(b Binding) []catalog.Column {
	if len(b.Expressions) != 1 || len(b.Expressions[0]) != 1 {
		return nil
	}
	call, ok := b.Expressions[0][0].(plan.SQLRoutineExpr)
	if !ok || len(call.Procedure.SQL.OutputFormals) == 0 {
		return nil
	}
	tuple := call.Signature.Result.Type
	columns := make([]catalog.Column, tuple.CompositeLen())
	for i := range columns {
		member := tuple.CompositeMember(i)
		columns[i] = catalog.Column{Name: member.Name, Usage: member.Usage}
	}
	return columns
}
