package engine

import "stuntgres/internal/engine/ids"

// Frozen action-owned modifiers are definitions to refresh, not protected
// enclosing statement spools. One mapper reconnects declarations and all scans.
func refreshRuleActionCTEs(auth AuthorizationView, b binding) (binding, error) {
	if len(b.ctes) == 0 {
		return b, nil
	}
	mapper := newRuleTreeMapper(auth.Catalog, nil, 0, nil, ruleTreeMapping{mutation: func(definition *cteDefinition) (binding, error) {
		body := *definition.mutation
		// Stored modifier recipes need a private execution workspace even when
		// catalog identity and every expression are unchanged. This mapper also
		// visits action-owned descendant definitions and reconnects their scans;
		// enclosing statement providers never enter this stored-action path.
		body.insert.conflict = body.insert.conflict.instantiate()
		if body.ref.Epoch != auth.Catalog.Epoch() {
			return body, ids.ErrCatalogReplaced
		}
		current, ok := auth.Catalog.Relation(body.ref.Address)
		if !ok {
			return body, ids.ErrCatalogReplaced
		}
		var err error
		body, err = refreshRuleBinding(auth.Catalog, body, current)
		if err != nil {
			return body, err
		}
		body.storage = auth.engine.authorizationStorageLocked(auth, body.ref.Address)
		return refreshRuleSources(auth, body)
	}})
	mapped, err := mapRuleBindingRecipes(b, mapper)
	mapped.rule.ctes = nil
	return mapped, err
}
