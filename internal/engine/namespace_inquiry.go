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
	if s.Catalog != nil {
		auth.Recapture(e.PrivateCatalogLocked(s.Catalog))
	}
	if s.Catalog != nil && temporaryNamespace(auth) == (ids.ObjectAddress{}) {
		for a, d := range s.Catalog.Descriptors {
			if ns, ok := d.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == auth.Session.SessionToken {
				auth.StagedTemporary = a
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
	path, pending, err := ExplicitPath(auth)
	if err != nil || !pending {
		return auth, path, err
	}
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog.Clone()
	}
	ref, err := e.temporaryNamespaceStagedLocked(auth, changes)
	if err != nil {
		return auth, nil, err
	}
	s.Catalog = changes
	auth.Recapture(e.PrivateCatalogLocked(changes))
	auth.StagedTemporary = ref.Address
	s.Inquiry.CurrentSchemaResult = sqltype.Value{}
	s.Inquiry.searchPathResults = [2]sqltype.Value{}
	path, _, err = ExplicitPath(auth)
	return auth, path, err
}
