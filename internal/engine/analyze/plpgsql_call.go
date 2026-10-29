package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
)

// A datum records a lexical bind occurrence, not a syntax-based destination
// guess. Coercion must preserve a lexical input or its one selected field.
// This scratch dies with the binder and never enters an executable child list.
type plCallDatum struct {
	node   expr.Expr
	target proc.PLTarget
}

func (b *Binder) capturePLCallOutputs(selected sqlCallableSelection) {
	definition := selected.Procedure.RoutineDefinition()
	outputs := make([]PLCallOutput, len(definition.OutputFormals))
	for i, formal := range definition.OutputFormals {
		output := PLCallOutput{Target: proc.PLTarget{Slot: -1}, Name: definition.Formals[formal].Name, Formal: formal + 1}
		source := selected.OutputMap[i]
		if source.Start >= 0 && source.Count == 1 {
			node := selected.args[source.Start]
			for _, datum := range b.plDatums {
				if samePLCallDatum(node, datum.node) {
					output.Target = datum.target
					break
				}
			}
		}
		outputs[i] = output
	}
	b.plCallOutputs = outputs
}

func samePLCallDatum(selected, original expr.Expr) bool {
	if selected.SourcePosition() != original.SourcePosition() || selected.ResultUsage() != original.ResultUsage() {
		return false
	}
	switch node := selected.(type) {
	case plan.PLLocalExpr:
		datum, ok := original.(plan.PLLocalExpr)
		return ok && node.Value == datum.Value
	case plan.FieldExpr:
		datum, ok := original.(plan.FieldExpr)
		return ok && node.Attribute == datum.Attribute && node.Index == datum.Index && samePLCallDatum(node.Base, datum.Base)
	}
	return false
}
