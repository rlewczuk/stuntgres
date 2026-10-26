package rolecmds

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

// Session.mu is held, and all revision checks have reserved rollback capacity.
func touchOperationIdentityLocked(op *session.Operation) {
	t := op.Session.Transactions
	scope := core.ActiveScopeOfLease(op.Lease)
	if (scope != nil && scope.Catalog != nil || t.RetainsTransaction() || t.State == session.TxActive) && !t.IdentityCaptured {
		t.IdentityEntry = op.Session.Authorization.State
		t.IdentityCaptured = true
	}
}

// A selected ACL tail on a private actual-role root is a mixed journal. Reserve
// its fresh record through the existing allocator, just like actual role
// installation; ACL-only journals retain their unreserved logical delta.
// Caller owns Engine.mu and this statement's newly cloned role record.
func reserveMixedACLRevisionLocked(e *core.Engine, c *session.Journal) error {
	if c.Roles.SameAuthority(c.RolesBase) {
		return nil
	}
	revision, err := e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		return err
	}
	c.Roles.Revision = max(c.Roles.Revision, revision)
	e.RoleStore.AdvanceRevisionFrontierLocked(c.Roles.Revision)
	return nil
}
