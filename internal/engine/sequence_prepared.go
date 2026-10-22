package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
)

// journalOf returns the identity of a private catalog journal; nil stays nil.
func journalOf(c *session.Journal) sequence.JournalIdentity {
	if c == nil {
		return nil
	}
	return c
}

func (e *Engine) sequenceEnvironmentLocked(auth AuthorizationView) (sequence.Environment, error) {
	key := sequence.PathKey{Role: auth.State.CurrentUser, Authorization: auth.Revision, Roles: auth.RoleRecord.Revision, Namespace: auth.Catalog.State().NamespaceGeneration(), Epoch: auth.Catalog.Epoch(), Temp: temporaryNamespace(auth), Prefix: auth.pathPrefix, Database: auth.Catalog.Get(catalog.CurrentDatabaseAddress)}
	if auth.session != nil && auth.session.Transactions != nil {
		key.Private = journalOf(auth.session.Transactions.Catalog)
	}
	if auth.settings != nil {
		auth.settings.Mu.Lock()
		key.Text = auth.settings.TypedValue("search_path").Text
		auth.settings.Mu.Unlock()
	} else {
		key.Text = e.settings.Definitions()["search_path"].BootValue().Text
	}
	if auth.session != nil {
		if environment, ok := payloadOf(auth.session).sequences.CachedPath(key); ok {
			return environment, nil
		}
	}
	path, pending, err := explicitPath(auth)
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

// sequenceObservationLocked captures what prepared sequence literals are
// analyzed and validated against. Caller holds Engine.mu; it computes the
// effective path, so callers build it only for a statement with literals.
func (e *Engine) sequenceObservationLocked(auth AuthorizationView) (sequence.Observation, error) {
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return sequence.Observation{}, err
	}
	return sequence.Observation{Domain: e.catalog.Domain, Read: auth.Catalog, Session: auth.session.SessionToken, Journal: journalOf(auth.session.Transactions.Catalog), Path: path, Memory: &payloadOf(auth.session).sequences}, nil
}
func (e *Engine) sequenceAnalysisLocked(auth AuthorizationView, constants []sequence.Constant) (sequence.Analysis, error) {
	if len(constants) == 0 {
		return sequence.Analysis{}, nil
	}
	o, err := e.sequenceObservationLocked(auth)
	if err != nil {
		return sequence.Analysis{}, err
	}
	return sequence.NewAnalysis(o, constants), nil
}
func (e *Engine) sequenceAnalysisValidLocked(auth AuthorizationView, analysis *sequence.Analysis) (bool, error) {
	if analysis == nil || len(analysis.Constants) == 0 {
		return true, nil
	}
	o, err := e.sequenceObservationLocked(auth)
	if err != nil {
		return false, err
	}
	return analysis.Valid(o)
}
