package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Engine.mu is held. Every inquiry sees the statement's private candidate before
// its transaction overlay; neither lookup nor evaluation publishes shared state.
func (s *StatementScope) namespaceAuthorizationLocked(e *Engine) (AuthorizationView, error) {
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return auth, err
	}
	if s.catalog != nil {
		auth.Recapture(e.privateCatalogLocked(s.catalog))
	}
	if s.catalog != nil && temporaryNamespace(auth) == (ids.ObjectAddress{}) {
		for a, d := range s.catalog.Descriptors {
			if ns, ok := d.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == auth.session.token {
				auth.stagedTemporary = a
				break
			}
		}
	}
	return auth, nil
}

// Resolve pending pg_temp only when a non-NULL schema inquiry is demanded.
// Pair creation joins the existing statement candidate and ordinary Finish owns
// publication or abort. Prepare and no-pending paths create no catalog stage.
func (s *StatementScope) schemaInquiryPathLocked(e *Engine) (AuthorizationView, []ids.ObjectAddress, error) {
	auth, err := s.namespaceAuthorizationLocked(e)
	if err != nil {
		return auth, nil, err
	}
	path, pending, err := e.explicitPath(auth)
	if err != nil || !pending {
		return auth, path, err
	}
	changes := s.catalog
	if changes == nil {
		changes = cloneCatalogChanges(s.owner.catalog)
	}
	ref, err := e.temporaryNamespaceStagedLocked(auth, changes)
	if err != nil {
		return auth, nil, err
	}
	s.catalog = changes
	auth.Recapture(e.privateCatalogLocked(changes))
	auth.stagedTemporary = ref.Address
	s.inquiry.currentSchemaResult = sqltype.Value{}
	s.inquiry.searchPathResults = [2]sqltype.Value{}
	path, _, err = e.explicitPath(auth)
	return auth, path, err
}
