package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

type identitySnapshot struct {
	database, current, session, system sqltype.Value
	currentMissing, sessionMissing     authorization.PrincipalRef
	currentPrincipal                   authorization.PrincipalRef
}

func (c *EvalContext) IdentityValue(kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.liveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: identity lacks statement authority")
	}
	return host.IdentityValue(c, kind, result)
}

func (h *statementHost) IdentityValue(c *EvalContext, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	identity := s.identity
	if c.query != nil && c.query.source.authority != nil && c.query.source.authority.expression != nil {
		identity = c.query.source.authority.expression.identity
	}
	if s.foreignActionAuth != nil {
		auth := s.foreignActionAuth
		role, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return sqltype.Value{}, err
		}
		name, _ := auth.Catalog.LookupType(sqltype.Name)
		identity.current, identity.currentMissing = sqltype.String(name, role.Name), authorization.PrincipalRef{}
	}
	switch kind {
	case functions.IdentityCatalog, functions.IdentityDatabase:
		return identity.database, nil
	case functions.IdentityRole, functions.IdentityCurrentUser, functions.IdentityUser:
		if identity.currentMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", identity.currentMissing.ID()))
		}
		return identity.current, nil
	case functions.IdentitySessionUser:
		if identity.sessionMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", identity.sessionMissing.ID()))
		}
		return identity.session, nil
	case functions.IdentitySystemUser:
		return identity.system, nil
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
	s.identity = identitySnapshot{database: sqltype.String(name, s.op.session.databaseName), system: sqltype.Null(text), currentPrincipal: auth.State.CurrentUser}
	if sessionExists {
		s.identity.session = sqltype.String(name, session.Name)
	} else {
		s.identity.sessionMissing = auth.State.SessionUser
	}
	if currentExists {
		s.identity.current = sqltype.String(name, current.Name)
	} else {
		s.identity.currentMissing = auth.State.CurrentUser
	}
	return nil
}

// Match current_user's expression identity, not the view's relation check owner.
func (c *EvalContext) expressionPrincipal() authorization.PrincipalRef {
	s := scopeOf(c.liveHost())
	if s.foreignActionAuth != nil {
		return s.foreignActionAuth.State.CurrentUser
	}
	if c.query != nil && c.query.source.authority != nil && c.query.source.authority.expression != nil {
		return c.query.source.authority.expression.identity.currentPrincipal
	}
	return s.identity.currentPrincipal
}
