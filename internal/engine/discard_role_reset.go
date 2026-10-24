package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
)

// discardRoleReset returns the identity DISCARD ALL installs: the session and
// current user become the authenticated role, or the RESET ROLE target.
// Caller holds Session.Mu.
func discardRoleReset(a *session.AuthorizationState) authorization.RoleState {
	reset := a.State
	reset.SessionUser, reset.CurrentUser = reset.Authenticated, reset.Authenticated
	if reset.ResetRole != (authorization.PrincipalRef{}) {
		reset.CurrentUser = reset.ResetRole
	}
	return reset
}

// validateRoleResetLocked checks revision headroom and that both target
// principals still exist in roles. DISCARD ALL runs it before TEMP staging
// and again in the combined publisher, closing the gap between role-identity
// validation and irreversible publication. Caller holds Session.Mu.
func validateRoleResetLocked(a *session.AuthorizationState, reset authorization.RoleState, roles authorization.Read) error {
	if err := session.RoleRevisionHeadroom(a, 3); err != nil {
		return err
	}
	for _, principal := range []authorization.PrincipalRef{reset.Authenticated, reset.CurrentUser} {
		if _, err := roles.Descriptor(principal); err != nil {
			return err
		}
	}
	return nil
}

// discardRoleResetPending reports whether op is publishing DISCARD ALL.
func discardRoleResetPending(op *session.Operation) bool {
	return op.DiscardRoleReset.Authenticated != (authorization.PrincipalRef{})
}

// finishDiscardRoleResetLocked installs the validated DISCARD ALL identity
// after publication. Caller holds Session.Mu.
func finishDiscardRoleResetLocked(op *session.Operation) {
	if discardRoleResetPending(op) {
		op.Session.Authorization.State = op.DiscardRoleReset
		op.Session.Authorization.Revision++
	}
}
