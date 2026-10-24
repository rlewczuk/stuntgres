package session

import (
	"stuntgres/internal/engine/authorization"
)

// DiscardRoleReset returns the identity DISCARD ALL installs: the session and
// current user become the authenticated role, or the RESET ROLE target.
// Caller holds Session.Mu.
func DiscardRoleReset(a *AuthorizationState) authorization.RoleState {
	reset := a.State
	reset.SessionUser, reset.CurrentUser = reset.Authenticated, reset.Authenticated
	if reset.ResetRole != (authorization.PrincipalRef{}) {
		reset.CurrentUser = reset.ResetRole
	}
	return reset
}

// ValidateRoleResetLocked checks revision headroom and that both target
// principals still exist in roles. DISCARD ALL runs it before TEMP staging
// and again in the combined publisher, closing the gap between role-identity
// validation and irreversible publication. Caller holds Session.Mu.
func ValidateRoleResetLocked(a *AuthorizationState, reset authorization.RoleState, roles authorization.Read) error {
	if err := RoleRevisionHeadroom(a, 3); err != nil {
		return err
	}
	for _, principal := range []authorization.PrincipalRef{reset.Authenticated, reset.CurrentUser} {
		if _, err := roles.Descriptor(principal); err != nil {
			return err
		}
	}
	return nil
}

// DiscardRoleResetPending reports whether op is publishing DISCARD ALL.
func (op *Operation) DiscardRoleResetPending() bool {
	return op.DiscardRoleReset.Authenticated != (authorization.PrincipalRef{})
}

// FinishDiscardRoleResetLocked installs the validated DISCARD ALL identity
// after publication. Caller holds Session.Mu.
func (op *Operation) FinishDiscardRoleResetLocked() {
	if op.DiscardRoleResetPending() {
		op.Session.Authorization.State = op.DiscardRoleReset
		op.Session.Authorization.Revision++
	}
}
