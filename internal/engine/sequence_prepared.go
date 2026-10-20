package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
)

// journalOf returns the identity of a private catalog journal; nil stays nil.
func journalOf(c *catalogChanges) sequence.JournalIdentity {
	if c == nil {
		return nil
	}
	return c
}

func (e *Engine) sequenceEnvironmentLocked(auth AuthorizationView) (sequence.Environment, error) {
	key := sequence.PathKey{Role: auth.State.CurrentUser, Authorization: auth.Revision, Roles: auth.RoleRecord.Revision, Namespace: auth.Catalog.State().NamespaceGeneration(), Epoch: auth.Catalog.Epoch(), Temp: temporaryNamespace(auth), Prefix: auth.pathPrefix, Database: auth.Catalog.Get(catalog.CurrentDatabaseAddress)}
	if auth.session != nil && auth.session.transactions != nil {
		key.Private = journalOf(auth.session.transactions.catalog)
	}
	if auth.settings != nil {
		auth.settings.mu.Lock()
		key.Text = auth.settings.typedValue("search_path").Text
		auth.settings.mu.Unlock()
	} else {
		key.Text = e.settings.Definitions()["search_path"].BootValue().Text
	}
	if auth.session != nil {
		if environment, ok := payloadOf(auth.session).sequences.CachedPath(key); ok {
			return environment, nil
		}
	}
	path, pending, err := e.explicitPath(auth)
	if err != nil {
		return sequence.Environment{}, err
	}
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	result := sequence.Environment{Namespaces: path, PendingTemp: pending, ImplicitCatalog: !slices.Contains(path, catalog), ImplicitTemp: key.Temp != (ids.ObjectAddress{}) && !slices.Contains(path, key.Temp)}
	if auth.session != nil {
		payloadOf(auth.session).sequences.StorePathLocked(key, result)
	}
	return result, nil
}
func (e *Engine) sequenceAnalysisLocked(auth AuthorizationView, constants []sequence.Constant) (sequence.Analysis, error) {
	if len(constants) == 0 {
		return sequence.Analysis{}, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return sequence.Analysis{}, err
	}
	return sequence.Analysis{Constants: constants, Path: path, Private: journalOf(auth.session.transactions.catalog), Session: auth.session.token, Namespace: auth.Catalog.State().NamespaceGeneration()}, nil
}
func (e *Engine) sequenceAnalysisValidLocked(auth AuthorizationView, analysis *sequence.Analysis) (bool, error) {
	if analysis == nil || len(analysis.Constants) == 0 {
		return true, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return false, err
	}
	changed := analysis.Session != auth.session.token || analysis.Private != journalOf(auth.session.transactions.catalog) || analysis.Namespace != auth.Catalog.State().NamespaceGeneration() || !sequence.SameEnvironment(path, analysis.Path)
	for _, constant := range analysis.Constants {
		ref := constant.Ref
		if ref.Domain() != e.catalog.Domain {
			return false, ids.ErrCatalogReplaced
		}
		if constant.Temporary != (ids.SessionToken{}) && constant.Temporary != auth.session.token {
			return false, ids.ErrCatalogReplaced
		}
		if ref.Epoch != auth.Catalog.Epoch() {
			// Native replacement permits only the original owner's exact live temp.
			d, ok := auth.Catalog.Relation(ref.Address)
			if !ok || constant.Temporary != auth.session.token || d.RelationKind != catalog.SequenceRelation {
				return false, ids.ErrCatalogReplaced
			}
			ns, ok := auth.Catalog.Namespace(d.Namespace())
			if !ok || ns.Temporary != constant.Temporary {
				return false, ids.ErrCatalogReplaced
			}
		}
		d, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			if constant.Stamp != 0 {
				changed = true
			}
			continue // Missing numeric regclass input is valid until relation opening.
		}
		stamp := preparedDefinitionStamp(d, auth.session)
		if stamp != constant.Stamp {
			changed = true
		}
	}
	return !changed, nil
}

// Native replacement refresh is provenance, not a license to rescue original
// SQL by name. Keep the original epoch on an exact retained temporary capture,
// even when another dependency forces this group's source to be reanalyzed.
func (e *Engine) pinSequenceReplacementLocked(auth AuthorizationView, previous *sequence.Analysis, constants []sequence.Constant) error {
	if previous == nil {
		return nil
	}
	for _, old := range previous.Constants {
		if old.Ref.Epoch == auth.Catalog.Epoch() {
			continue
		}
		found := false
		for i := range constants {
			current := &constants[i]
			if current.Offset != old.Offset {
				continue
			}
			if old.Temporary != auth.session.token || current.Temporary != old.Temporary || current.Ref.Domain() != old.Ref.Domain() || current.Ref.Address != old.Ref.Address {
				return ids.ErrCatalogReplaced
			}
			current.Ref.Epoch = old.Ref.Epoch
			found = true
			break
		}
		if !found {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
