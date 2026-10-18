package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// Only sequence literal analysis is mutable; descriptions, ASTs and cursors are not.
// Engine.mu and physical-session admission serialize this cell and path cache.
type sequenceAnalysis struct {
	constants []sequenceConstant
	path      sequenceEnvironment
	private   *catalogChanges
	session   ids.SessionToken
	// Namespace callbacks invalidate analysis even when the effective path is equal.
	namespace uint64
}
type sequenceEnvironment struct {
	namespaces                                 []ids.ObjectAddress
	implicitCatalog, implicitTemp, pendingTemp bool
}
type sequencePathKey struct {
	text                 string
	role                 authorization.PrincipalRef
	authorization, roles uint64
	namespace            uint64
	epoch                ids.CatalogEpoch
	temp, prefix         ids.ObjectAddress
	private              *catalogChanges
	database             Descriptor
}
type sequencePathCache struct {
	valid       bool
	key         sequencePathKey
	environment sequenceEnvironment
}

func (e *Engine) sequenceEnvironmentLocked(auth AuthorizationView) (sequenceEnvironment, error) {
	key := sequencePathKey{role: auth.state.CurrentUser, authorization: auth.revision, roles: auth.roleRecord.revision, namespace: auth.catalog.state.namespaceGeneration, epoch: auth.catalog.Epoch(), temp: temporaryNamespace(auth), prefix: auth.pathPrefix, database: auth.catalog.Get(currentDatabaseAddress)}
	if auth.session != nil && auth.session.transactions != nil {
		key.private = auth.session.transactions.catalog
	}
	if auth.settings != nil {
		auth.settings.mu.Lock()
		key.text = auth.settings.typedValue("search_path").text
		auth.settings.mu.Unlock()
	} else {
		key.text = e.settingsDefinitions["search_path"].bootValue.text
	}
	if auth.session != nil && auth.session.sequencePath.valid && auth.session.sequencePath.key == key {
		return auth.session.sequencePath.environment, nil
	}
	path, pending, err := e.explicitPath(auth)
	if err != nil {
		return sequenceEnvironment{}, err
	}
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	result := sequenceEnvironment{namespaces: path, pendingTemp: pending, implicitCatalog: !slices.Contains(path, catalog), implicitTemp: key.temp != (ids.ObjectAddress{}) && !slices.Contains(path, key.temp)}
	if auth.session != nil {
		auth.session.sequencePath = sequencePathCache{valid: true, key: key, environment: result}
	}
	return result, nil
}
func sameSequenceEnvironment(a, b sequenceEnvironment) bool {
	return a.implicitCatalog == b.implicitCatalog && a.implicitTemp == b.implicitTemp && a.pendingTemp == b.pendingTemp && slices.Equal(a.namespaces, b.namespaces)
}
func (e *Engine) sequenceAnalysisLocked(auth AuthorizationView, constants []sequenceConstant) (sequenceAnalysis, error) {
	if len(constants) == 0 {
		return sequenceAnalysis{}, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return sequenceAnalysis{}, err
	}
	return sequenceAnalysis{constants: constants, path: path, private: auth.session.transactions.catalog, session: auth.session.token, namespace: auth.catalog.state.namespaceGeneration}, nil
}
func (e *Engine) sequenceAnalysisValidLocked(auth AuthorizationView, analysis *sequenceAnalysis) (bool, error) {
	if analysis == nil || len(analysis.constants) == 0 {
		return true, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return false, err
	}
	changed := analysis.session != auth.session.token || analysis.private != auth.session.transactions.catalog || analysis.namespace != auth.catalog.state.namespaceGeneration || !sameSequenceEnvironment(path, analysis.path)
	for _, constant := range analysis.constants {
		ref := constant.ref
		if ref.Domain() != e.catalog.domain {
			return false, ids.ErrCatalogReplaced
		}
		if constant.temporary != (ids.SessionToken{}) && constant.temporary != auth.session.token {
			return false, ids.ErrCatalogReplaced
		}
		if ref.Epoch != auth.catalog.Epoch() {
			// Native replacement permits only the original owner's exact live temp.
			d, ok := auth.catalog.Relation(ref.Address)
			if !ok || constant.temporary != auth.session.token || d.RelationKind != sequenceRelation {
				return false, ids.ErrCatalogReplaced
			}
			ns, ok := auth.catalog.Namespace(d.namespace)
			if !ok || ns.temporary != constant.temporary {
				return false, ids.ErrCatalogReplaced
			}
		}
		d, ok := auth.catalog.Relation(ref.Address)
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
		if old.ref.Epoch == auth.catalog.Epoch() {
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
