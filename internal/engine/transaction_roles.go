package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Engine.mu protects committed and pending immutable role roots; statement
// admission prevents concurrent mutation of this session's transaction overlay.
func (e *Engine) operationRolesLocked(op *session.Operation) *access.RoleRecord {
	if op != nil {
		return e.sessionRolesLocked(op.Session)
	}
	return e.roles.published
}
func (e *Engine) sessionRolesLocked(session *session.Session) *access.RoleRecord {
	if session != nil && session.Transactions != nil {
		if c := session.Transactions.Catalog; c != nil && c.Roles != nil {
			return c.Roles
		}
	}
	return e.roles.published
}

// The operation/session publication preflight has passed before this call.
func (e *Engine) installOperationRolesLocked(op *session.Operation, next *access.RoleRecord) {
	t := op.Session.Transactions
	if !t.RetainsTransaction() {
		e.roles.published = next
		return
	}
	c := t.Catalog.Clone()
	if c.RolesBase == nil {
		c.RolesBase = e.roles.published
	}
	c.Roles = next
	t.Catalog = c
}

// Session.mu is held, and all revision checks have reserved rollback capacity.
func (e *Engine) touchOperationIdentityLocked(op *session.Operation) {
	t := op.Session.Transactions
	if (t.RetainsTransaction() || t.State == session.TxActive) && !t.IdentityCaptured {
		t.IdentityEntry = op.Session.Authorization.State
		t.IdentityCaptured = true
	}
}

// Called only at final publication under Engine.mu, with the prepared combined
// catalog validated against the current committed root. Early statement checks
// must not forbid a later supported removal of an owner's own dependencies.
func (e *Engine) roleRetirementPreflightLocked(c *session.Journal, catalog *catalog.State) error {
	for ref := range c.RetiredRoles {
		if access.DefaultRoleDependencies(e.catalog.read(catalog), e.roles.read(e.catalog.owner(), e.catalog.read(catalog), c.Roles), ref, nil) {
			return ids.ErrCatalogReplaced
		}
		for _, descriptor := range catalog.Descriptors() {
			if access.DescriptorRoleDependencies(descriptor, ref, nil) {
				return ids.ErrCatalogReplaced
			}
		}
		for edge := range c.Roles.Root.Read(authorization.PrincipalRef{}).Edges() {
			if edge.Role == ref || edge.Member == ref || edge.Grantor == ref {
				return ids.ErrCatalogReplaced
			}
		}
		if len(e.roles.referencingFeatures(ref)) > 0 {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
