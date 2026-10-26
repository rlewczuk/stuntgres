package core

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
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ACLAuthority() *sqltype.ACLAuthority {
	scope := h.scope()
	return &scope.Engine().RoleStore.ACLAuthority
}

func (h *statementHost) ObjectACL(c *exec.Context, address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := aclInquiryAuthorizationLocked(c, scope, e)
	if err != nil {
		return sqltype.Value{}, err
	}
	acl, err := access.RawObjectACL(auth.Catalog, address)
	if err != nil {
		return sqltype.Value{}, err
	}
	return pgcatalog.ClassACL(&e.Catalog.Owner, auth.Roles, result, acl)
}

func (h *statementHost) RoleMembership(c *exec.Context, actor *functions.AccessRole, target functions.AccessRole, privilege string) (bool, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
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
	return authorization.HasRole(c.Context, auth.Roles, &e.RoleStore.Domain, actorID, targetID, mask)
}

func (h *statementHost) RowSecurityActive(c *exec.Context, relation functions.AccessRelation) (bool, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
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
			return false, SQLError("42602", "invalid name syntax")
		}
		name, err = regobj.ClassQualifiedName(name, relation.Name, auth.Session.DatabaseName)
		if err != nil {
			return false, err
		}
		ref, err := e.ResolveRelationReference(auth, name, true)
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
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := aclInquiryAuthorizationLocked(c, scope, e)
	if err != nil {
		return false, false, err
	}
	if kind == functions.AccessParameter {
		ordinary, options, failure := authorization.ParseObjectPrivileges(privilege, authorization.SetPrivilege|authorization.AlterSystemPrivilege)
		if failure != nil {
			return false, false, failure
		}
		actorID := expressionPrincipal(c).ID()
		if actor != nil {
			if !actor.ByOID && actor.Name == "public" {
				actorID = 0
			} else if actorID, failure = regobj.AccessRoleID(auth.Roles, *actor); failure != nil {
				return false, false, failure
			}
		}
		principal := auth.View.Owner.Principals.Principal(actorID)
		if r, live := auth.Roles.Find(principal); live && r.Superuser {
			return true, true, nil
		}
		address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: role.CanonicalInquiryParameter(object.Name)})
		if address == (ids.ObjectAddress{}) {
			return false, true, nil
		}
		return auth.View.ObjectPrivilege(c.Context, auth, actorID, address, ordinary, options)
	}
	actorID := expressionPrincipal(c).ID()
	if actor != nil {
		if !actor.ByOID && actor.Name == "public" {
			actorID = 0
		} else if actorID, err = regobj.AccessRoleID(auth.Roles, *actor); err != nil {
			return false, false, err
		}
	}
	if kind == functions.AccessLargeObject {
		ordinary, options, failure := authorization.ParseObjectPrivileges(privilege, authorization.SelectPrivilege|authorization.UpdatePrivilege)
		if failure != nil {
			return false, false, failure
		}
		if ordinary&authorization.UpdatePrivilege == 0 {
			auth.Recapture(scope.largeObjectRead)
		}
		address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: ids.OID(object.OID)}
		if _, present := auth.Catalog.Descriptor(address); !present {
			return false, false, nil
		}
		scope.Op.Session.Settings.Mu.Lock()
		compat := scope.Op.Session.Settings.Value("lo_compat_privileges") == "on"
		scope.Op.Session.Settings.Mu.Unlock()
		if compat {
			return true, true, nil
		}
		return auth.View.ObjectPrivilege(c.Context, auth, actorID, address, ordinary, options)
	}
	var class ids.OID
	var noun string
	var allowed authorization.Privileges
	switch kind {
	case functions.AccessFunction:
		class, noun, allowed = ids.ProcedureClassID, "function", authorization.ExecutePrivilege
	case functions.AccessLanguage:
		class, noun, allowed = ids.LanguageClassID, "language", authorization.UsagePrivilege
	case functions.AccessForeignWrapper:
		class, noun, allowed = ids.ForeignWrapperClassID, "foreign-data wrapper", authorization.UsagePrivilege
	case functions.AccessServer:
		class, noun, allowed = ids.ForeignServerClassID, "server", authorization.UsagePrivilege
	case functions.AccessDatabase:
		class, noun, allowed = ids.DatabaseClassID, "database", authorization.CreatePrivilege|authorization.ConnectPrivilege|authorization.TemporaryPrivilege
	case functions.AccessSchema:
		class, noun, allowed = ids.NamespaceClassID, "schema", authorization.CreatePrivilege|authorization.UsagePrivilege
	case functions.AccessType:
		class, noun, allowed = ids.TypeClassID, "type", authorization.UsagePrivilege
	case functions.AccessTablespace:
		class, noun, allowed = ids.TablespaceClassID, "tablespace", authorization.CreatePrivilege
	default:
		return false, false, fmt.Errorf("stuntgres: invalid object privilege inquiry")
	}
	address := ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(object.OID)}
	if !object.ByOID {
		if kind == functions.AccessType {
			auth.State.CurrentUser = expressionPrincipal(c)
			input, failure := CaptureRegtypeInput(auth, scope.Op)
			if failure != nil {
				return false, false, failure
			}
			oid, failure := input.ResolveRegtype(c.Context, object.Name)
			if failure != nil {
				return false, false, failure
			}
			if oid == 0 {
				return false, false, SQLError("42704", fmt.Sprintf(`type "%s" does not exist`, object.Name))
			}
			address.ObjectID = ids.OID(oid)
		} else if kind == functions.AccessFunction {
			auth.State.CurrentUser = expressionPrincipal(c)
			types, failure := CaptureRegtypeInput(auth, scope.Op)
			if failure != nil {
				return false, false, failure
			}
			input, failure := CaptureRoutineInput(auth, types)
			if failure != nil {
				return false, false, failure
			}
			oid, failure := input.ResolveRegprocedure(c.Context, object.Name)
			if failure != nil {
				return false, false, failure
			}
			if oid == 0 {
				return false, false, SQLError("42883", fmt.Sprintf(`function "%s" does not exist`, object.Name))
			}
			address.ObjectID = ids.OID(oid)
		} else {
			address = auth.Catalog.NameAddress(ids.NameKey{ClassID: class, Name: object.Name})
			if address == (ids.ObjectAddress{}) {
				state := "42704"
				if kind == functions.AccessDatabase {
					state = "3D000"
				} else if kind == functions.AccessSchema {
					state = "3F000"
				}
				return false, false, SQLError(state, fmt.Sprintf(`%s "%s" does not exist`, noun, object.Name))
			}
		}
	}
	ordinary, options, err := authorization.ParseObjectPrivileges(privilege, allowed)
	if err != nil {
		return false, false, err
	}
	value, exists, err := auth.View.ObjectPrivilege(c.Context, auth, actorID, address, ordinary, options)
	if err == nil && !exists && !object.ByOID && kind == functions.AccessFunction {
		err = SQLError("XX000", fmt.Sprintf("cache lookup failed for function %d", address.ObjectID))
	} else if err == nil && !exists && !object.ByOID && kind == functions.AccessType {
		err = SQLError("42704", fmt.Sprintf("type with OID %d does not exist", address.ObjectID))
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
	if scope.Catalog != nil {
		auth.Recapture(e.PrivateCatalogLocked(scope.Catalog))
	}
	return auth, nil
}
