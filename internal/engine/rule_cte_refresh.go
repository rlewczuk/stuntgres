package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// Frozen action-owned modifiers are definitions to refresh, not protected
// enclosing statement spools. One mapper reconnects declarations and all scans.
func refreshRuleActionCTEs(auth AuthorizationView, b binding) (binding, error) {
	if len(b.CTEs) == 0 {
		return b, nil
	}
	mapper := newRuleTreeMapper(auth.Catalog, nil, 0, nil, ruleTreeMapping{mutation: func(definition *plan.CTEDefinition) (binding, error) {
		mutation, _ := analyze.MutationBinding(definition)
		body := *mutation
		// Stored modifier recipes need a private execution workspace even when
		// catalog identity and every expression are unchanged. This mapper also
		// visits action-owned descendant definitions and reconnects their scans;
		// enclosing statement providers never enter this stored-action path.
		body.Insert.Conflict = body.Insert.Conflict.Clone()
		if body.Ref.Epoch != auth.Catalog.Epoch() {
			return body, ids.ErrCatalogReplaced
		}
		current, ok := auth.Catalog.Relation(body.Ref.Address)
		if !ok {
			return body, ids.ErrCatalogReplaced
		}
		var err error
		body, err = refreshRuleBinding(auth.Catalog, body, current)
		if err != nil {
			return body, err
		}
		body.Storage = auth.engine.authorizationStorageLocked(auth, body.Ref.Address)
		return refreshRuleSources(auth, body)
	}})
	mapped, err := mapRuleBindingRecipes(b, mapper)
	mapped.Rule.CTEs = nil
	return mapped, err
}
