package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/role"
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

// roleRetirementPreflightLocked runs role.RetirementPreflightLocked over
// the engine's catalog owner, principal domain and registered features.
func (e *Engine) roleRetirementPreflightLocked(c *session.Journal, state *catalog.State) error {
	return role.RetirementPreflightLocked(c, e.catalog.owner(), &e.roles.domain, state, e.roles.referencingFeatures)
}
