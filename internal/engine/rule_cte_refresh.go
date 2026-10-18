package engine

// Frozen action-owned modifiers are definitions to refresh, not protected
// enclosing statement spools. One mapper reconnects declarations and all scans.
func refreshRuleActionCTEs(auth AuthorizationView, b binding) (binding, error) {
	if len(b.ctes) == 0 {
		return b, nil
	}
	mapper := newRuleTreeMapper(auth.catalog, nil, 0, nil, ruleTreeMapping{mutation: func(definition *cteDefinition) (binding, error) {
		body := *definition.mutation
		// Stored modifier recipes need a private execution workspace even when
		// catalog identity and every expression are unchanged. This mapper also
		// visits action-owned descendant definitions and reconnects their scans;
		// enclosing statement providers never enter this stored-action path.
		body.conflict = body.conflict.instantiate()
		if body.ref.Epoch != auth.catalog.Epoch() {
			return body, ErrCatalogReplaced
		}
		current, ok := auth.catalog.Relation(body.ref.Address)
		if !ok {
			return body, ErrCatalogReplaced
		}
		var err error
		body, err = refreshRuleBinding(auth.catalog, body, current)
		if err != nil {
			return body, err
		}
		body.storage = auth.engine.authorizationStorageLocked(auth, body.ref.Address)
		return refreshRuleSources(auth, body)
	}})
	mapped, err := mapRuleBindingRecipes(b, mapper)
	mapped.ruleCTEs = nil
	return mapped, err
}
