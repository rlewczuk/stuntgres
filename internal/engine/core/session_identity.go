package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
)

// Capture the identities under the binding catalog/authorization authority;
// the snapshot changes only when the capture succeeds.
func (s *StatementScope) CaptureIdentityLocked(auth AuthorizationView) error {
	identity, err := exec.CaptureIdentity(auth.Catalog, auth.Roles, auth.State, s.Op.Session.DatabaseName)
	if err != nil {
		return err
	}
	s.Identity = identity
	return nil
}

// Match current_user's expression identity, not the view's relation check owner.
func expressionPrincipal(c *exec.Context) authorization.PrincipalRef {
	s := ScopeOf(c.LiveHost())
	if s.Op.Restricted == nil && s.ForeignActionAuth != nil {
		return s.ForeignActionAuth.State.CurrentUser
	}
	if _, active := s.Op.EffectiveUserOwner(); active {
		return s.Identity.CurrentPrincipal
	}
	if c.Query != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Expression != nil {
		return c.Query.Source.Authority.Expression.Identity.CurrentPrincipal
	}
	return s.Identity.CurrentPrincipal
}
