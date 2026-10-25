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
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ACLAuthority() *sqltype.ACLAuthority {
	scope := h.scope()
	return &scope.engine().roles.ACLAuthority
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
	return pgcatalog.ClassACL(&e.catalog.Owner, auth.Roles, result, acl)
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
		if actorID, err = regobj.AccessRoleID(auth.Roles, *actor); err != nil {
			return false, err
		}
	}
	targetID, err := regobj.AccessRoleID(auth.Roles, target)
	if err != nil {
		return false, err
	}
	mask, err := authorization.ParseRolePrivileges(privilege)
	if err != nil {
		return false, err
	}
	return authorization.HasRole(c.Context, auth.Roles, &e.roles.Domain, actorID, targetID, mask)
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
		name, err = regobj.ClassQualifiedName(name, relation.Name, auth.session.DatabaseName)
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

func (h *statementHost) ObjectPrivilege(c *exec.Context, kind functions.AccessKind, actor *functions.AccessRole, object functions.AccessRole, privilege string) (bool, bool, error) {
	scope := h.scope()
	e := scope.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := aclInquiryAuthorizationLocked(c, scope, e)
	if err != nil {
		return false, false, err
	}
	actorID := expressionPrincipal(c).ID()
	if actor != nil {
		if !actor.ByOID && actor.Name == "public" {
			actorID = 0
		} else if actorID, err = regobj.AccessRoleID(auth.Roles, *actor); err != nil {
			return false, false, err
		}
	}
	var class ids.OID
	var noun, token string
	switch kind {
	case functions.AccessFunction:
		class, noun, token = ids.ProcedureClassID, "function", "EXECUTE"
	case functions.AccessLanguage:
		class, noun, token = ids.LanguageClassID, "language", "USAGE"
	case functions.AccessForeignWrapper:
		class, noun, token = ids.ForeignWrapperClassID, "foreign-data wrapper", "USAGE"
	case functions.AccessServer:
		class, noun, token = ids.ForeignServerClassID, "server", "USAGE"
	default:
		return false, false, fmt.Errorf("stuntgres: invalid object privilege inquiry")
	}
	address := ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(object.OID)}
	if !object.ByOID {
		if kind == functions.AccessFunction {
			auth.State.CurrentUser = expressionPrincipal(c)
			types, failure := captureRegtypeInput(auth, scope.op)
			if failure != nil {
				return false, false, failure
			}
			input, failure := captureRoutineInput(auth, types)
			if failure != nil {
				return false, false, failure
			}
			oid, failure := input.ResolveRegprocedure(c.Context, object.Name)
			if failure != nil {
				return false, false, failure
			}
			if oid == 0 {
				return false, false, sqlError("42883", fmt.Sprintf(`function "%s" does not exist`, object.Name))
			}
			address.ObjectID = ids.OID(oid)
		} else {
			address = auth.Catalog.NameAddress(ids.NameKey{ClassID: class, Name: object.Name})
			if address == (ids.ObjectAddress{}) {
				return false, false, sqlError("42704", fmt.Sprintf(`%s "%s" does not exist`, noun, object.Name))
			}
		}
	}
	ordinary, options, err := authorization.ParseObjectPrivileges(privilege, token)
	if err != nil {
		return false, false, err
	}
	value, exists, err := auth.View.ObjectPrivilege(c.Context, actorID, address, ordinary, options)
	if err == nil && !exists && !object.ByOID && kind == functions.AccessFunction {
		err = sqlError("XX000", fmt.Sprintf("cache lookup failed for function %d", address.ObjectID))
	}
	return value, exists, err
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
