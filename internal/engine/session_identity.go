package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type sessionIdentityKind uint8

const (
	identityCatalog sessionIdentityKind = iota
	identityDatabase
	identityRole
	identityCurrentUser
	identitySessionUser
	identityUser
	identitySystemUser
	identityCurrentSchema
)

func (k sessionIdentityKind) spelling() string {
	return [...]string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user", "current_schema"}[k]
}

type identitySnapshot struct {
	database, current, session, system sqltype.Value
	currentMissing, sessionMissing     authorization.PrincipalRef
	currentPrincipal                   authorization.PrincipalRef
}
type identityExpression struct {
	expr.Info
	kind         sessionIdentityKind
	ordinaryCall bool
}

func (e identityExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.liveScope() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: identity lacks statement authority")
	}
	s := c.liveScope()
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
	switch e.kind {
	case identityCatalog, identityDatabase:
		return identity.database, nil
	case identityRole, identityCurrentUser, identityUser:
		if identity.currentMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", identity.currentMissing.ID()))
		}
		return identity.current, nil
	case identitySessionUser:
		if identity.sessionMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", identity.sessionMissing.ID()))
		}
		return identity.session, nil
	case identitySystemUser:
		return identity.system, nil
	case identityCurrentSchema:
		if s.foreignActionAuth == nil && s.ruleActionAuth == nil && s.currentSchemaResult.Type() != nil {
			return s.currentSchemaResult, nil
		}
		engine := s.op.session.Engine()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		auth, path, err := s.schemaInquiryPathLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		result := sqltype.Null(e.Usage.Type)
		if len(path) > 0 {
			result = sqltype.String(e.Usage.Type, auth.Catalog.Get(path[0]).Name())
		}
		if s.foreignActionAuth == nil && s.ruleActionAuth == nil {
			s.currentSchemaResult = result
		}
		return result, nil
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
	s := c.liveScope()
	if s.foreignActionAuth != nil {
		return s.foreignActionAuth.State.CurrentUser
	}
	if c.query != nil && c.query.source.authority != nil && c.query.source.authority.expression != nil {
		return c.query.source.authority.expression.identity.currentPrincipal
	}
	return s.identity.currentPrincipal
}

func (b *expressionBinder) bindIdentity(name string, pos parser.Position) (expr.Expr, error) {
	var kind sessionIdentityKind
	switch name {
	case "current_catalog":
		kind = identityCatalog
	case "current_database":
		kind = identityDatabase
	case "current_role":
		kind = identityRole
	case "current_user":
		kind = identityCurrentUser
	case "session_user":
		kind = identitySessionUser
	case "user":
		kind = identityUser
	case "system_user":
		kind = identitySystemUser
	case "current_schema":
		kind = identityCurrentSchema
	default:
		return nil, fmt.Errorf("stuntgres: invalid identity keyword")
	}
	id := sqltype.Name
	if kind == identitySystemUser {
		id = sqltype.Text
	}
	info := b.info(b.typ(id).Usage(), pos, expr.Properties{Volatility: expr.Stable})
	if kind == identitySystemUser {
		var err error
		info, err = b.builtinRoutineInfo(info, catalogRoutineProvider, "system_user")
		if err != nil {
			return nil, err
		}
	}
	return identityExpression{Info: info, kind: kind}, nil
}

func (b *expressionBinder) bindIdentityFunction(raw parser.FunctionExpr) (expr.Expr, error) {
	// PostgreSQL transforms children before looking up the callee namespace.
	// Valid zero-argument calls need neither diagnostic storage nor child work.
	var names []string
	if len(raw.Args) != 0 {
		var err error
		names, _, err = b.functionArgumentTypeNames(raw)
		if err != nil {
			return nil, err
		}
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	valid := name == "current_database" || name == "current_user" || name == "session_user" || name == "system_user" || name == "current_schema"
	if !visible || !valid || len(raw.Args) != 0 || raw.Variadic {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	node, err := b.bindIdentity(name, raw.Pos)
	if err != nil {
		return nil, err
	}
	identity := node.(identityExpression)
	identity.ordinaryCall = true
	if identity.RoutineReference == nil {
		identity.Info, err = b.builtinRoutineInfo(identity.Info, catalogRoutineProvider, name)
		if err != nil {
			return nil, err
		}
	}
	return identity, nil
}
