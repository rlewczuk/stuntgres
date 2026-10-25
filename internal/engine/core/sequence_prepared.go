package core

import (
	"slices"
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
	key := sequence.PathKey{Role: auth.State.CurrentUser, Authorization: auth.Revision, Roles: auth.RoleRecord.Revision, Namespace: auth.Catalog.State().NamespaceGeneration(), Epoch: auth.Catalog.Epoch(), Temp: temporaryNamespace(auth), Prefix: auth.PathPrefix, Database: auth.Catalog.Get(auth.Owner.DatabaseAddress())}
	if auth.Session != nil && auth.Session.Transactions != nil {
		key.Private = journalOf(auth.Session.Transactions.Catalog)
	}
	if auth.settings != nil {
		auth.settings.Mu.Lock()
		key.Text = auth.settings.TypedValue("search_path").Text
		auth.settings.Mu.Unlock()
	} else {
		key.Text = e.Settings.Definitions()["search_path"].BootValue().Text
	}
	if auth.Session != nil {
		if environment, ok := PayloadOf(auth.Session).Sequences.CachedPath(key); ok {
			return environment, nil
		}
	}
	path, pending, err := ExplicitPath(auth)
	if err != nil {
		return sequence.Environment{}, err
	}
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	result := sequence.Environment{Namespaces: path, PendingTemp: pending, ImplicitCatalog: !slices.Contains(path, catalog), ImplicitTemp: key.Temp != (ids.ObjectAddress{}) && !slices.Contains(path, key.Temp)}
	if auth.Session != nil {
		PayloadOf(auth.Session).Sequences.StorePathLocked(key, result)
	}
	return result, nil
}

// SequenceObservationLocked captures what prepared sequence literals are
// analyzed and validated against. Caller holds Engine.mu; it computes the
// effective path, so callers build it only for a statement with literals.
func (e *Engine) SequenceObservationLocked(auth AuthorizationView) (sequence.Observation, error) {
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return sequence.Observation{}, err
	}
	return sequence.Observation{Domain: e.Catalog.Domain, Read: auth.Catalog, Session: auth.Session.SessionToken, Journal: journalOf(auth.Session.Transactions.Catalog), Path: path, Memory: &PayloadOf(auth.Session).Sequences}, nil
}
func (e *Engine) sequenceAnalysisLocked(auth AuthorizationView, constants []sequence.Constant) (sequence.Analysis, error) {
	if len(constants) == 0 {
		return sequence.Analysis{}, nil
	}
	o, err := e.SequenceObservationLocked(auth)
	if err != nil {
		return sequence.Analysis{}, err
	}
	return sequence.NewAnalysis(o, constants), nil
}
func (e *Engine) sequenceAnalysisValidLocked(auth AuthorizationView, analysis *sequence.Analysis) (bool, error) {
	if analysis == nil || len(analysis.Constants) == 0 {
		return true, nil
	}
	o, err := e.SequenceObservationLocked(auth)
	if err != nil {
		return false, err
	}
	return analysis.Valid(o)
}
