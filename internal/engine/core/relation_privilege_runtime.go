package core

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
)

// RelationPrivilege borrows coherent private catalog/role authority under Mu.
// No descriptor, principal or decision escapes the call.
func (h *statementHost) RelationPrivilege(c *exec.Context, kind functions.AccessKind, actor *functions.AccessRole, relation functions.AccessRelation, column functions.AccessColumn, privilege string) (bool, bool, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := aclInquiryAuthorizationLocked(c, scope, e)
	if err != nil {
		return false, false, err
	}
	if err := c.Context.Err(); err != nil {
		return false, false, err
	}
	actorID := expressionPrincipal(c).ID()
	if actor != nil {
		if !actor.ByOID && actor.Name == "public" {
			actorID = 0
		} else {
			actorID, err = regobj.AccessRoleID(auth.Roles, *actor)
			if err != nil {
				return false, false, err
			}
		}
	}
	allowed := authorization.TableGrantPrivileges
	switch kind {
	case functions.AccessTable:
	case functions.AccessSequence:
		allowed = authorization.SelectPrivilege | authorization.UsagePrivilege | authorization.UpdatePrivilege
	case functions.AccessColumnPrivilege, functions.AccessAnyColumn:
		allowed = authorization.ColumnGrantPrivileges
	default:
		return false, false, fmt.Errorf("stuntgres: invalid relation privilege inquiry")
	}
	var ordinary, options authorization.Privileges
	if kind == functions.AccessSequence {
		ordinary, options, err = authorization.ParseRelationPrivileges(privilege, allowed)
		if err != nil {
			return false, false, err
		}
	}
	auth.State.CurrentUser = expressionPrincipal(c)
	var d *catalog.Relation
	if relation.ByOID {
		d, _ = auth.Catalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(relation.OID)})
	} else {
		name, failure := parser.ParseRegclassName(relation.Name)
		if failure != nil {
			return false, false, SQLError("42602", "invalid name syntax")
		}
		name, failure = regobj.ClassQualifiedName(name, relation.Name, auth.Session.DatabaseName)
		if failure != nil {
			return false, false, failure
		}
		ref, failure := e.ResolveRelationReference(auth, name, true)
		if failure != nil {
			return false, false, failure
		}
		d, _ = auth.Catalog.Relation(ref.Address)
	}
	var sub int32
	if kind == functions.AccessColumnPrivilege {
		sub = column.Number
	}
	if kind == functions.AccessColumnPrivilege && !column.ByNumber {
		sub = 0
		if d != nil {
			found := false
			for i, label := range d.Columns {
				if label.Name == column.Name {
					sub, found = d.Attributes[i], true
					break
				}
			}
			if !found && d.HasSystemAttributes() {
				for i, label := range [...]string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
					if label == column.Name {
						sub, found = -int32(i+1), true
						break
					}
				}
			}
			if !found {
				for _, dropped := range d.ColumnMeta.Dropped {
					if dropped.Name == column.Name {
						found = true
						break
					}
				}
			}
			if !found {
				return false, false, SQLError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column.Name, d.Name()))
			}
		}
	}
	if kind != functions.AccessSequence {
		ordinary, options, err = authorization.ParseRelationPrivileges(privilege, allowed)
		if err != nil {
			return false, false, err
		}
	}
	if d == nil {
		return false, false, nil
	}
	if kind == functions.AccessSequence && d.RelationKind != catalog.SequenceRelation {
		name := d.Name()
		if !relation.ByOID {
			name = relation.Name
		}
		return false, false, SQLError("42809", fmt.Sprintf(`"%s" is not a sequence`, name))
	}
	if kind == functions.AccessColumnPrivilege && !(sub > 0 && d.HasAttribute(sub) || sub >= -6 && sub <= -1 && d.HasSystemAttributes()) {
		return false, false, nil
	}
	value, err := auth.View.RelationPrivilege(c.Context, actorID, d, sub, kind == functions.AccessAnyColumn, ordinary, options)
	return value, true, err
}
