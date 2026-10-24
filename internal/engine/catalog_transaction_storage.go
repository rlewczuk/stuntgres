package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Caller holds Engine.mu; never derive entry authority from a later live view.
func (v AuthorizationView) catalogAuthorityLocked(ref ids.ObjectRef, descriptor catalog.Descriptor) *session.CatalogAuthority {
	return &session.CatalogAuthority{Session: v.session, Authorization: v.Revision, Roles: v.RoleRecord.Revision, Ref: ref, Descriptor: descriptor, DatabaseAuthority: v.Catalog.Get(v.Owner.DatabaseAddress()), DatabasePredecessor: v.DatabasePredecessor}
}

// catalogAuthorityPreflightLocked fences a statement's catalog authority (nil
// passes). Caller holds Engine.mu and the authority session's mutex.
func (e *Engine) catalogAuthorityPreflightLocked(a *session.CatalogAuthority, c *session.Journal) error {
	if a == nil {
		return nil
	}
	return a.PreflightLocked(e.catalog.State, e.sessionRolesLocked(a.Session), e.roles.Published, e.catalog.DatabaseAddress(), c)
}

// Callers hold Engine.mu; private storage is never installed before Finish/Commit.
func (e *Engine) privateStorageLocked(c *session.Journal, a ids.ObjectAddress) *storage.Relation {
	return session.PrivateStorage(&e.storage, c, a)
}
