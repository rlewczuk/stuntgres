package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// journalOf returns the identity of a private catalog journal; nil stays nil.
func journalOf(c *catalogChanges) journalIdentity {
	if c == nil {
		return nil
	}
	return c
}

func (e *Engine) sequenceEnvironmentLocked(auth AuthorizationView) (sequenceEnvironment, error) {
	key := sequencePathKey{role: auth.State.CurrentUser, authorization: auth.Revision, roles: auth.RoleRecord.Revision, namespace: auth.Catalog.State().NamespaceGeneration(), epoch: auth.Catalog.Epoch(), temp: temporaryNamespace(auth), prefix: auth.pathPrefix, database: auth.Catalog.Get(catalog.CurrentDatabaseAddress)}
	if auth.session != nil && auth.session.transactions != nil {
		key.private = journalOf(auth.session.transactions.catalog)
	}
	if auth.settings != nil {
		auth.settings.mu.Lock()
		key.text = auth.settings.typedValue("search_path").Text
		auth.settings.mu.Unlock()
	} else {
		key.text = e.settings.Definitions()["search_path"].BootValue().Text
	}
	if auth.session != nil {
		if environment, ok := auth.session.sequences.cachedPath(key); ok {
			return environment, nil
		}
	}
	path, pending, err := e.explicitPath(auth)
	if err != nil {
		return sequenceEnvironment{}, err
	}
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	result := sequenceEnvironment{namespaces: path, pendingTemp: pending, implicitCatalog: !slices.Contains(path, catalog), implicitTemp: key.temp != (ids.ObjectAddress{}) && !slices.Contains(path, key.temp)}
	if auth.session != nil {
		auth.session.sequences.storePathLocked(key, result)
	}
	return result, nil
}
func (e *Engine) sequenceAnalysisLocked(auth AuthorizationView, constants []sequenceConstant) (sequenceAnalysis, error) {
	if len(constants) == 0 {
		return sequenceAnalysis{}, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return sequenceAnalysis{}, err
	}
	return sequenceAnalysis{constants: constants, path: path, private: journalOf(auth.session.transactions.catalog), session: auth.session.token, namespace: auth.Catalog.State().NamespaceGeneration()}, nil
}
func (e *Engine) sequenceAnalysisValidLocked(auth AuthorizationView, analysis *sequenceAnalysis) (bool, error) {
	if analysis == nil || len(analysis.constants) == 0 {
		return true, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return false, err
	}
	changed := analysis.session != auth.session.token || analysis.private != journalOf(auth.session.transactions.catalog) || analysis.namespace != auth.Catalog.State().NamespaceGeneration() || !sameSequenceEnvironment(path, analysis.path)
	for _, constant := range analysis.constants {
		ref := constant.ref
		if ref.Domain() != e.catalog.Domain {
			return false, ids.ErrCatalogReplaced
		}
		if constant.temporary != (ids.SessionToken{}) && constant.temporary != auth.session.token {
			return false, ids.ErrCatalogReplaced
		}
		if ref.Epoch != auth.Catalog.Epoch() {
			// Native replacement permits only the original owner's exact live temp.
			d, ok := auth.Catalog.Relation(ref.Address)
			if !ok || constant.temporary != auth.session.token || d.RelationKind != catalog.SequenceRelation {
				return false, ids.ErrCatalogReplaced
			}
			ns, ok := auth.Catalog.Namespace(d.Namespace())
			if !ok || ns.Temporary != constant.temporary {
				return false, ids.ErrCatalogReplaced
			}
		}
		d, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			if constant.stamp != 0 {
				changed = true
			}
			continue // Missing numeric regclass input is valid until relation opening.
		}
		stamp := preparedDefinitionStamp(d, auth.session)
		if stamp != constant.stamp {
			changed = true
		}
	}
	return !changed, nil
}

// Native replacement refresh is provenance, not a license to rescue original
// SQL by name. Keep the original epoch on an exact retained temporary capture,
// even when another dependency forces this group's source to be reanalyzed.
func (e *Engine) pinSequenceReplacementLocked(auth AuthorizationView, previous *sequenceAnalysis, constants []sequenceConstant) error {
	if previous == nil {
		return nil
	}
	for _, old := range previous.constants {
		if old.ref.Epoch == auth.Catalog.Epoch() {
			continue
		}
		found := false
		for i := range constants {
			current := &constants[i]
			if current.offset != old.offset {
				continue
			}
			if old.temporary != auth.session.token || current.temporary != old.temporary || current.ref.Domain() != old.ref.Domain() || current.ref.Address != old.ref.Address {
				return ids.ErrCatalogReplaced
			}
			current.ref.Epoch = old.ref.Epoch
			found = true
			break
		}
		if !found {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
