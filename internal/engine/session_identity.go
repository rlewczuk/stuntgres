package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) IdentityValue(c *EvalContext, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	identity := s.identity
	if c.Query != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Expression != nil {
		identity = c.Query.Source.Authority.Expression.Identity
	}
	if s.foreignActionAuth != nil {
		auth := s.foreignActionAuth
		role, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return sqltype.Value{}, err
		}
		name, _ := auth.Catalog.LookupType(sqltype.Name)
		identity.Current, identity.CurrentMissing = sqltype.String(name, role.Name), authorization.PrincipalRef{}
	}
	switch kind {
	case functions.IdentityCatalog, functions.IdentityDatabase:
		return identity.Database, nil
	case functions.IdentityRole, functions.IdentityCurrentUser, functions.IdentityUser:
		if identity.CurrentMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", identity.CurrentMissing.ID()))
		}
		return identity.Current, nil
	case functions.IdentitySessionUser:
		if identity.SessionMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", identity.SessionMissing.ID()))
		}
		return identity.Session, nil
	case functions.IdentitySystemUser:
		return identity.System, nil
	case functions.IdentityCurrentSchema:
		if s.foreignActionAuth == nil && s.ruleActionAuth == nil && s.inquiry.currentSchemaResult.Type() != nil {
			return s.inquiry.currentSchemaResult, nil
		}
		engine := s.op.session.Engine()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		auth, path, err := s.schemaInquiryPathLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		resultValue := sqltype.Null(result)
		if len(path) > 0 {
			resultValue = sqltype.String(result, auth.Catalog.Get(path[0]).Name())
		}
		if s.foreignActionAuth == nil && s.ruleActionAuth == nil {
			s.inquiry.currentSchemaResult = resultValue
		}
		return resultValue, nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid identity expression")
}

// Capture the seven selected identities under the binding catalog/authorization
// authority. Their leaves retain no session and perform no live role lookup.
func (s *StatementScope) captureIdentityLocked(auth AuthorizationView) error {
	name, _ := auth.Catalog.LookupType(sqltype.Name)
	text, _ := auth.Catalog.LookupType(sqltype.Text)
	if err := auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
		return err
	}
	current, currentExists := auth.Roles.Find(auth.State.CurrentUser)
	if err := auth.Roles.ValidatePrincipal(auth.State.SessionUser); err != nil {
		return err
	}
	session, sessionExists := auth.Roles.Find(auth.State.SessionUser)
	s.identity = identitySnapshot{Database: sqltype.String(name, s.op.session.databaseName), System: sqltype.Null(text), CurrentPrincipal: auth.State.CurrentUser}
	if sessionExists {
		s.identity.Session = sqltype.String(name, session.Name)
	} else {
		s.identity.SessionMissing = auth.State.SessionUser
	}
	if currentExists {
		s.identity.Current = sqltype.String(name, current.Name)
	} else {
		s.identity.CurrentMissing = auth.State.CurrentUser
	}
	return nil
}

// Match current_user's expression identity, not the view's relation check owner.
func expressionPrincipal(c *EvalContext) authorization.PrincipalRef {
	s := scopeOf(c.LiveHost())
	if s.foreignActionAuth != nil {
		return s.foreignActionAuth.State.CurrentUser
	}
	if c.Query != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Expression != nil {
		return c.Query.Source.Authority.Expression.Identity.CurrentPrincipal
	}
	return s.identity.CurrentPrincipal
}
