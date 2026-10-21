package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The role default is an internal physical-session hook, not a registered GUC
// or a public connector option. Providers run without publication locks.
func (e *Engine) initialRole(ctx context.Context, authenticated authorization.PrincipalRef, provider settings.SessionDefaultProvider) (authorization.PrincipalRef, error) {
	if provider == nil {
		return authorization.PrincipalRef{}, nil
	}
	name, present, err := provider.SessionDefault(ctx, settings.Definition{Name: "role", BootDefault: "none", Type: settings.String})
	if err != nil {
		return authorization.PrincipalRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return authorization.PrincipalRef{}, err
	}
	if !present || name == "none" {
		return authorization.PrincipalRef{}, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)
	target, ok := roles.Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", name))
	}
	if err := authorization.ValidateInitialRole(roles, authenticated, target.Ref); err != nil {
		return authorization.PrincipalRef{}, err
	}
	return target.Ref, nil
}

func (e *Engine) executeRole(op *session.Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if selection, ok := stmt.(*parser.RoleSelection); ok && selection.Local && implicit {
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	if err = op.Context().Err(); err != nil {
		return Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.ObjectGrant:
		return e.objectGrantLocked(op, v, s)
	case *parser.AlterDefaultPrivileges:
		return e.alterDefaultPrivilegesLocked(op, v, s)
	case *parser.RoleMembership:
		return e.membershipLocked(op, v, s)
	case *parser.RoleSelection:
		target, err := role.SetRoleTarget(v.View, s)
		if err != nil {
			return Result{}, err
		}
		sess := op.Session
		settings := sess.Settings
		settings.Mu.Lock()
		defer settings.Mu.Unlock()
		sess.Mu.Lock()
		defer sess.Mu.Unlock()
		a := sess.Authorization
		// Reserve failure unwind, retained savepoints and outer restoration.
		if err := session.RoleRevisionHeadroom(a, uint64(len(sess.Transactions.Savepoints))+3); err != nil {
			return Result{}, err
		}
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		e.touchOperationIdentityLocked(op)
		settings.TouchRole(a.State.CurrentUser, s.Local)
		a.State.CurrentUser = target
		a.Revision++
		return Result{}, nil
	case *parser.SessionAuthorization:
		target, err := role.SessionAuthorizationTarget(v.View, s)
		if err != nil {
			return Result{}, err
		}
		sess := op.Session
		settings := sess.Settings
		settings.Mu.Lock()
		defer settings.Mu.Unlock()
		sess.Mu.Lock()
		defer sess.Mu.Unlock()
		a := sess.Authorization
		if err := session.RoleRevisionHeadroom(a, uint64(len(sess.Transactions.Savepoints))+3); err != nil {
			return Result{}, err
		}
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		e.touchOperationIdentityLocked(op)
		settings.TouchRole(a.State.CurrentUser, false)
		a.State.SessionUser = target
		a.State.CurrentUser = target
		a.Revision++
		return Result{}, nil
	case *parser.DropRole:
		plan, err := role.PlanDropRoles(ddlHost{e: e, op: op}, v, s)
		if err != nil {
			return Result{}, err
		}
		return e.publishDroppedRolesLocked(op, plan)
	}
	return Result{}, fmt.Errorf("stuntgres: invalid role statement")
}

func dependencyDescription(auth AuthorizationView, dep access.RoleDependency) string {
	view := auth.Catalog
	if dep.Kind == 'r' {
		relation := view.MustRelation(dep.PolicyTable)
		return "target of policy " + catalog.DependencyIdentifier(dep.PolicyName) + " on table " + relationDiagnosticName(auth, relation)
	}
	if dep.Description != "" {
		return dep.Description
	}
	address := dep.Address
	address.SubID = 0
	d := view.Get(address)
	name := catalog.DependencyIdentifier(d.Name())
	if _, ok := d.(*catalog.Namespace); ok {
		name = d.Name()
	}
	kind := "schema "
	switch d.(type) {
	case *catalog.Database:
		kind = "database "
	case *catalog.Parameter:
		kind = "parameter "
		name = d.Name()
	case *catalog.Tablespace:
		kind = "tablespace "
	case *catalog.Extension:
		kind = "extension "
	case *catalog.Procedure:
		kind = "function "
	case *catalog.TSDictionary:
		kind = "text search dictionary "
	}
	if foreign, ok := d.(*catalog.ForeignObject); ok {
		kind = fdw.Label(foreign.Address().ClassID) + " "
		if foreign.Address().ClassID == ids.ForeignMappingClassID {
			kind = "user mapping for "
			mapped, _ := auth.Roles.Descriptor(foreign.MappedRole)
			name = catalog.DependencyIdentifier(mapped.Name) + " on server " + catalog.DependencyIdentifier(view.Get(foreign.Server).Name())
		}
	}
	if d.Kind() == ids.ProcedureObject || d.Kind() == ids.TSDictionaryObject {
		path, _ := auth.engine.referencePath(auth)
		name = strings.TrimPrefix(dependencyObject(view, d.Address(), path), kind)
	}
	if relation, ok := d.(*catalog.Relation); ok {
		kind = catalog.RelationDiagnosticKind(relation) + " "
		name = relationDiagnosticName(auth, relation)
		if dep.Address.SubID != 0 {
			// getObjectDescription interpolates the raw attribute name; only
			// getRelationDescription applies diagnostic identifier quoting.
			for slot, attribute := range relation.Attributes {
				if attribute == dep.Address.SubID {
					kind = "column " + relation.Columns[slot].Name + " of table "
					break
				}
			}
		}
		if relation.RelationKind == catalog.CompositeRelation {
			path, _ := auth.engine.referencePath(auth)
			typ := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
			kind, name = "type ", catalog.TypeDiagnosticNameAt(view, typ, path)
		}
	}
	if typ, ok := d.(*catalog.Type); ok && (typ.SQLType.IsUserDefined() || typ.SQLType.IsArray() && typ.SQLType.Element().IsDomain()) {
		path, _ := auth.engine.referencePath(auth)
		kind = "type "
		name = catalog.TypeDiagnosticNameAt(view, typ, path)
	}
	if c, ok := d.(*catalog.Collation); ok {
		path, _ := auth.engine.referencePath(auth)
		kind = "collation "
		name = strings.TrimPrefix(dependencyObject(view, c.Address(), path), "collation ")
	}
	if c, ok := d.(*catalog.Conversion); ok {
		path, _ := auth.engine.referencePath(auth)
		kind = "conversion "
		name = strings.TrimPrefix(dependencyObject(view, c.Address(), path), "conversion ")
	}
	prefix := "privileges for "
	if dep.Kind == 'o' {
		prefix = "owner of "
	}
	return prefix + kind + name
}

// publishDroppedRolesLocked installs a decided DROP ROLE: the role record
// joins the operation, and a retained transaction remembers the retired roles.
func (e *Engine) publishDroppedRolesLocked(op *session.Operation, plan role.DropRolesPlan) (Result, error) {
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	e.installOperationRolesLocked(op, plan.Roles)
	if t := op.Session.Transactions; t.RetainsTransaction() {
		// installOperationRolesLocked cloned the same catalog owner; no saved
		// candidate shares this mutable set. Standalone publication needs none.
		if t.Catalog.RetiredRoles == nil {
			t.Catalog.RetiredRoles = make(map[authorization.PrincipalRef]struct{}, len(plan.Retired))
		}
		for _, ref := range plan.Retired {
			t.Catalog.RetiredRoles[ref] = struct{}{}
		}
	}
	return Result{}, nil
}
