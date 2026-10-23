package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

// Modifier bodies keep their own frozen action/view. Only finalized ancestor
// naming facts cross that boundary; executable frames and payloads do not.
func (r *QueryRenderer) ruleModifierSQL(ctx context.Context, read catalog.Read, statement parser.Statement, parent *ruleRangeTable) (string, error) {
	action := r.ruleAction
	if action == nil {
		return "", fmt.Errorf("stuntgres: missing retained rule modifier binding")
	}
	for _, definition := range action.binding.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if definition.Statement != statement || !ok {
			continue
		}
		presentation := action.modifierPresentations[statement]
		if presentation == nil {
			return "", fmt.Errorf("stuntgres: missing retained rule modifier presentation")
		}
		nested := *action
		nested.RuleAction = RuleAction{syntax: statement, binding: *mutation, presentation: presentation, modifierPresentations: action.modifierPresentations}
		nested.policy = r.Presentation
		nested.names, nested.parentNames = nil, parent
		return ruleActionSQL(ctx, read, nested)
	}
	return "", fmt.Errorf("stuntgres: missing retained rule modifier binding")
}
