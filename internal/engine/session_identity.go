package engine

import (
	"fmt"
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

type identitySnapshot struct{ database, current, session, system sqltype.Value }
type identityExpression struct {
	expressionInfo
	kind         sessionIdentityKind
	ordinaryCall bool
}

func (e identityExpression) Eval(c *EvalContext) (sqltype.Value, error) {
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
	switch e.kind {
	case identityCatalog, identityDatabase:
		return identity.database, nil
	case identityRole, identityCurrentUser, identityUser:
		return identity.current, nil
	case identitySessionUser:
		return identity.session, nil
	case identitySystemUser:
		return identity.system, nil
	case identityCurrentSchema:
		if s.currentSchemaResult.Type() != nil {
			return s.currentSchemaResult, nil
		}
		engine := s.op.session.Engine()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		auth, err := engine.authorizationLocked(s.op)
		if err != nil {
			return sqltype.Value{}, err
		}
		path, _, err := engine.explicitPath(auth)
		if err != nil {
			return sqltype.Value{}, err
		}
		s.currentSchemaResult = sqltype.Null(e.usage.Type)
		if len(path) > 0 {
			s.currentSchemaResult = sqltype.String(e.usage.Type, auth.catalog.state.descriptors[path[0]].Name())
		}
		return s.currentSchemaResult, nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid identity expression")
}

// Capture the seven selected identities under the binding catalog/authorization
// authority. Their leaves retain no session and perform no live role lookup.
func (s *StatementScope) captureIdentityLocked(auth AuthorizationView) error {
	name, _ := auth.catalog.LookupType(sqltype.Name)
	text, _ := auth.catalog.LookupType(sqltype.Text)
	current, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	session, err := auth.roles.Descriptor(auth.state.SessionUser)
	if err != nil {
		return err
	}
	s.identity = identitySnapshot{sqltype.String(name, s.op.session.databaseName), sqltype.String(name, current.Name), sqltype.String(name, session.Name), sqltype.Null(text)}
	return nil
}

func (b *expressionBinder) bindIdentity(name string, pos parser.Position) (BoundExpr, error) {
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
	return identityExpression{expressionInfo: b.info(b.typ(id).Usage(), pos, ExprProperties{Volatility: Stable}), kind: kind}, nil
}

func (b *expressionBinder) bindIdentityFunction(raw parser.FunctionExpr) (BoundExpr, error) {
	// PostgreSQL transforms children before looking up the callee namespace.
	// Valid zero-argument calls need neither diagnostic storage nor child work.
	var names []string
	if len(raw.Args) != 0 {
		var err error
		names, err = b.functionArgumentTypeNames(raw)
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
		return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	node, err := b.bindIdentity(name, raw.Pos)
	if err != nil {
		return nil, err
	}
	identity := node.(identityExpression)
	identity.ordinaryCall = true
	return identity, nil
}

// canSetRole follows only SET-authorized membership edges. Valid descriptors
// are required even for self/superuser shortcuts; stale identities fail closed.
func (r RoleRead) canSetRole(session, target PrincipalRef) (bool, error) {
	actor, err := r.Descriptor(session)
	if err != nil {
		return false, err
	}
	if _, err := r.Descriptor(target); err != nil {
		return false, err
	}
	if session == target || actor.Superuser {
		return true, nil
	}
	if len(r.state.edges) == 0 && r.implicitDatabaseOwner(session) == (PrincipalRef{}) {
		return false, nil
	}
	visited := map[PrincipalRef]bool{session: true}
	queue := []PrincipalRef{session}
	for len(queue) != 0 {
		member := queue[0]
		queue = queue[1:]
		for _, edge := range r.state.edges {
			if !edge.Set || edge.Member != member || visited[edge.Role] {
				continue
			}
			if _, err := r.Descriptor(edge.Role); err != nil {
				return false, err
			}
			if edge.Role == target {
				return true, nil
			}
			visited[edge.Role] = true
			queue = append(queue, edge.Role)
		}
		if next := r.implicitDatabaseOwner(member); next != (PrincipalRef{}) && !visited[next] {
			if _, err := r.Descriptor(next); err != nil {
				return false, err
			}
			if next == target {
				return true, nil
			}
			visited[next] = true
			queue = append(queue, next)
		}
	}
	return false, nil
}
