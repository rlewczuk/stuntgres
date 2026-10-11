package engine

// Engine.mu protects committed and pending immutable role roots; statement
// admission prevents concurrent mutation of this session's transaction overlay.
func (e *Engine) operationRolesLocked(op *Operation) *roleCatalog {
	if op != nil {
		return e.sessionRolesLocked(op.session)
	}
	return e.roles
}
func (e *Engine) sessionRolesLocked(session *Session) *roleCatalog {
	if session != nil && session.transactions != nil {
		if c := session.transactions.catalog; c != nil && c.roles != nil {
			return c.roles
		}
	}
	return e.roles
}

// The operation/session publication preflight has passed before this call.
func (e *Engine) installOperationRolesLocked(op *Operation, next *roleCatalog) {
	t := op.session.transactions
	if !t.retainedMessage {
		e.roles = next
		return
	}
	c := cloneCatalogChanges(t.catalog)
	if c.rolesBase == nil {
		c.rolesBase = e.roles
	}
	c.roles = next
	t.catalog = c
}

// Session.mu is held, and all revision checks have reserved rollback capacity.
func (e *Engine) touchOperationIdentityLocked(op *Operation) {
	t := op.session.transactions
	if (t.retainedMessage || t.state == TxActive) && !t.identityCaptured {
		t.identityEntry = op.session.authorization.state
		t.identityCaptured = true
	}
}

// Called only at final publication under Engine.mu, with the prepared combined
// catalog validated against the current committed root. Early statement checks
// must not forbid a later supported removal of an owner's own dependencies.
func (e *Engine) roleRetirementPreflightLocked(c *catalogChanges, catalog *catalogState) error {
	for ref := range c.retiredRoles {
		if defaultRoleDependencies(CatalogRead{e, catalog}, RoleRead{e, c.roles}, ref, nil) {
			return ErrCatalogReplaced
		}
		for _, descriptor := range catalog.descriptors {
			if descriptorRoleDependencies(descriptor, ref, nil) {
				return ErrCatalogReplaced
			}
		}
		for _, edge := range c.roles.edges {
			if edge.Role == ref || edge.Member == ref || edge.Grantor == ref {
				return ErrCatalogReplaced
			}
		}
		for _, refs := range e.roleReferences {
			for _, dependency := range refs {
				if dependency == ref {
					return ErrCatalogReplaced
				}
			}
		}
	}
	return nil
}
