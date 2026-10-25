package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) IdentityValue(c *exec.Context, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	identity := s.Identity
	if c.Query != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Expression != nil {
		identity = c.Query.Source.Authority.Expression.Identity
	}
	if s.ForeignActionAuth != nil {
		auth := s.ForeignActionAuth
		role, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return sqltype.Value{}, err
		}
		name, _ := auth.Catalog.LookupType(sqltype.Name)
		identity.Current, identity.CurrentMissing = sqltype.String(name, role.Name), authorization.PrincipalRef{}
	}
	if kind != functions.IdentityCurrentSchema {
		return identity.Value(kind)
	}
	if s.ForeignActionAuth == nil && s.ruleAction.Auth == nil && s.Inquiry.CurrentSchemaResult.Type() != nil {
		return s.Inquiry.CurrentSchemaResult, nil
	}
	engine := s.Engine()
	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	auth, path, err := s.schemaInquiryPathLocked(engine)
	if err != nil {
		return sqltype.Value{}, err
	}
	resultValue := sqltype.Null(result)
	if len(path) > 0 {
		resultValue = sqltype.String(result, auth.Catalog.Get(path[0]).Name())
	}
	if s.ForeignActionAuth == nil && s.ruleAction.Auth == nil {
		s.Inquiry.CurrentSchemaResult = resultValue
	}
	return resultValue, nil
}

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
	if s.ForeignActionAuth != nil {
		return s.ForeignActionAuth.State.CurrentUser
	}
	if c.Query != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Expression != nil {
		return c.Query.Source.Authority.Expression.Identity.CurrentPrincipal
	}
	return s.Identity.CurrentPrincipal
}
