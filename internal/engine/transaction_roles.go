package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Engine.mu protects committed and pending immutable role roots; statement
// admission prevents concurrent mutation of this session's transaction overlay.
func (e *Engine) operationRolesLocked(op *Operation) *roleCatalog {
	if op != nil {
		return e.sessionRolesLocked(op.session)
	}
	return e.roles.published
}
func (e *Engine) sessionRolesLocked(session *Session) *roleCatalog {
	if session != nil && session.transactions != nil {
		if c := session.transactions.catalog; c != nil && c.roles != nil {
			return c.roles
		}
	}
	return e.roles.published
}

// The operation/session publication preflight has passed before this call.
func (e *Engine) installOperationRolesLocked(op *Operation, next *roleCatalog) {
	t := op.session.transactions
	if !t.retainsTransaction() {
		e.roles.published = next
		return
	}
	c := cloneCatalogChanges(t.catalog)
	if c.rolesBase == nil {
		c.rolesBase = e.roles.published
	}
	c.roles = next
	t.catalog = c
}

// Session.mu is held, and all revision checks have reserved rollback capacity.
func (e *Engine) touchOperationIdentityLocked(op *Operation) {
	t := op.session.transactions
	if (t.retainsTransaction() || t.state == TxActive) && !t.identityCaptured {
		t.identityEntry = op.session.authorization.state
		t.identityCaptured = true
	}
}

// Called only at final publication under Engine.mu, with the prepared combined
// catalog validated against the current committed root. Early statement checks
// must not forbid a later supported removal of an owner's own dependencies.
func (e *Engine) roleRetirementPreflightLocked(c *catalogChanges, catalog *catalog.State) error {
	for ref := range c.retiredRoles {
		if access.DefaultRoleDependencies(e.catalog.read(catalog), e.roles.read(e.catalog.owner(), e.catalog.read(catalog), c.roles), ref, nil) {
			return ids.ErrCatalogReplaced
		}
		for _, descriptor := range catalog.Descriptors() {
			if access.DescriptorRoleDependencies(descriptor, ref, nil) {
				return ids.ErrCatalogReplaced
			}
		}
		for edge := range c.roles.Root.Read(authorization.PrincipalRef{}).Edges() {
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
