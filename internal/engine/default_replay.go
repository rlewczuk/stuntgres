package engine

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// One replay decision over an already-bound, validated definition, not DEFAULT
// admission or volatility policy. Captured closed expressions and the selected
// closed regclass result subset may install after their providers are created.
func defaultReplayDeferrable(definition columnDefault) bool {
	node := definition.expression
	if node == nil {
		return false
	}
	if definition.catalogAliasReferences {
		return true
	}
	if closedSequenceDefault(node) {
		eligible, captured := true, false
		walkBoundExpression(node, func(child BoundExpr) {
			if call, ok := child.(sequenceCallExpression); ok {
				captured = true
				if !slices.Contains(definition.dependencies, call.ref) {
					eligible = false
				}
			}
		})
		if captured {
			return eligible
		}
	}
	if node.Properties() != (ExprProperties{}) {
		return false
	}
	// The selected stored regclass result subset also includes opaque arrays,
	// domains and numeric coercion trees, without manufacturing DROP providers.
	if node.ResultUsage().Type.ContainsRegclass() && closedRegclassDefault(node) {
		return true
	}
	captured := false
	walkBoundExpression(node, func(child BoundExpr) {
		literal, ok := child.(literalExpression)
		if ok && literal.regclassRef != (ids.ObjectRef{}) &&
			literal.regclassRef.Address.ClassID == RelationClassID && literal.regclassRef.Address.SubID == 0 &&
			literal.ResultUsage().Type.ID() == sqltype.Regclass && !literal.value.IsNull() &&
			slices.Contains(definition.dependencies, literal.regclassRef) {
			captured = true
		}
	})
	return captured
}
