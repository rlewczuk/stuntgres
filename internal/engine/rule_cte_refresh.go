package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// Frozen action-owned modifiers are definitions to refresh, not protected
// enclosing statement spools. One mapper reconnects declarations and all scans.
func refreshRuleActionCTEs(auth ruleAuthority, b analyze.Binding) (analyze.Binding, error) {
	if len(b.CTEs) == 0 {
		return b, nil
	}
	mapper := stored.NewRuleTreeMapper(auth.Access().Catalog, nil, 0, nil, stored.RuleTreeMapping{Mutation: func(definition *plan.CTEDefinition) (analyze.Binding, error) {
		mutation, _ := analyze.MutationBinding(definition)
		body := *mutation
		// Stored modifier recipes need a private execution workspace even when
		// catalog identity and every expression are unchanged. This mapper also
		// visits action-owned descendant definitions and reconnects their scans;
		// enclosing statement providers never enter this stored-action path.
		body.Insert.Conflict = body.Insert.Conflict.Clone()
		if body.Ref.Epoch != auth.Access().Catalog.Epoch() {
			return body, ids.ErrCatalogReplaced
		}
		current, ok := auth.Access().Catalog.Relation(body.Ref.Address)
		if !ok {
			return body, ids.ErrCatalogReplaced
		}
		var err error
		body, err = stored.RefreshRuleBinding(auth.Access().Catalog, body, current)
		if err != nil {
			return body, err
		}
		body.Storage = auth.StorageLocked(body.Ref.Address)
		return refreshRuleSources(auth, body)
	}})
	mapped, err := stored.MapRuleBindingRecipes(b, mapper)
	mapped.Rule.CTEs = nil
	return mapped, err
}
