package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ACLAuthority() *sqltype.ACLAuthority {
	scope := h.scope()
	return &scope.engine().roles.aclAuthority
}

func (h *statementHost) ObjectACL(c *exec.Context, address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	e := scope.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := aclInquiryAuthorizationLocked(c, scope, e)
	if err != nil {
		return sqltype.Value{}, err
	}
	acl, err := access.RawObjectACL(auth.Catalog, address)
	if err != nil {
		return sqltype.Value{}, err
	}
	return pgcatalog.ClassACL(e.catalog.owner(), auth.Roles, result, acl)
}

func (h *statementHost) RoleMembership(c *exec.Context, actor *functions.AccessRole, target functions.AccessRole, privilege string) (bool, error) {
	scope := h.scope()
	e := scope.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := aclInquiryAuthorizationLocked(c, scope, e)
	if err != nil {
		return false, err
	}
	actorID := uint64(expressionPrincipal(c).ID())
	if actor != nil {
		if actorID, err = accessRoleArgument(auth, *actor); err != nil {
			return false, err
		}
	}
	targetID, err := accessRoleArgument(auth, target)
	if err != nil {
		return false, err
	}
	mask, err := authorization.ParseRolePrivileges(privilege)
	if err != nil {
		return false, err
	}
	return authorization.HasRole(c.Context, auth.Roles, &e.roles.domain, actorID, targetID, mask)
}

func (h *statementHost) RowSecurityActive(c *exec.Context, relation functions.AccessRelation) (bool, error) {
	scope := h.scope()
	e := scope.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := aclInquiryAuthorizationLocked(c, scope, e)
	if err != nil {
		return false, err
	}
	principal := expressionPrincipal(c)
	// Schema lookup belongs to this expression's user as well, not a rule or
	// view's independent relation-authorization identity.
	auth.State.CurrentUser = principal
	var d *catalog.Relation
	if relation.ByOID {
		d, _ = auth.Catalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(relation.OID)})
	} else {
		name, err := parser.ParseRegclassName(relation.Name)
		if err != nil {
			return false, sqlError("42602", "invalid name syntax")
		}
		name, err = regclassQualifiedName(name, relation.Name, auth.session.DatabaseName)
		if err != nil {
			return false, err
		}
		ref, err := e.resolveRelationReference(auth, name, true)
		if err != nil {
			return false, err
		}
		d, _ = auth.Catalog.Relation(ref.Address)
	}
	if d == nil {
		return false, nil
	}
	return access.RowSecurityApplies(auth.Roles, d, principal)
}

// aclInquiryAuthorizationLocked is the statement's write authorization over
// its private catalog, if any. The caller holds e.mu.
func aclInquiryAuthorizationLocked(c *exec.Context, scope *StatementScope, e *Engine) (AuthorizationView, error) {
	auth, err := scope.writeAuthorizationLocked(e)
	if err != nil {
		return AuthorizationView{}, err
	}
	if scope.catalog != nil {
		auth.Recapture(e.privateCatalogLocked(scope.catalog))
	}
	return auth, nil
}

func accessRoleArgument(auth AuthorizationView, role functions.AccessRole) (uint64, error) {
	if role.ByOID {
		return uint64(role.OID), nil
	}
	found, ok := auth.Roles.Lookup(role.Name)
	if !ok {
		return 0, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, role.Name))
	}
	return found.Ref.ID(), nil
}
