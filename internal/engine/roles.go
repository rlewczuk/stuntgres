package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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

func roleDiagnostic(code, message, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
}
func (e *Engine) executeRole(op *Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if selection, ok := stmt.(*parser.RoleSelection); ok && selection.Local && implicit {
		op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
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
		target := v.State.SessionUser
		if s.Reset && v.State.ResetRole != (authorization.PrincipalRef{}) {
			target = v.State.ResetRole
		} else if !s.Reset && s.Name != "none" {
			d, ok := v.Roles.Lookup(s.Name)
			if !ok {
				return Result{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", s.Name))
			}
			target = d.Ref
		}
		if s.Reset {
			if _, err := v.Roles.Descriptor(target); err != nil {
				return Result{}, err
			}
		} else {
			allowed := target == v.State.SessionUser
			if !allowed {
				allowed, err = v.Roles.CanSetRole(v.State.SessionUser, target)
				if err != nil {
					return Result{}, err
				}
			}
			if !allowed {
				return Result{}, sqlError("42501", fmt.Sprintf("permission denied to set role \"%s\"", s.Name))
			}
		}
		session := op.session
		settings := session.settings
		settings.mu.Lock()
		defer settings.mu.Unlock()
		session.mu.Lock()
		defer session.mu.Unlock()
		a := session.authorization
		// Reserve failure unwind, retained savepoints and outer restoration.
		if err := roleRevisionHeadroom(a, uint64(len(session.transactions.savepoints))+3); err != nil {
			return Result{}, err
		}
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		e.touchOperationIdentityLocked(op)
		settings.touchRole(a.state.CurrentUser, s.Local)
		a.state.CurrentUser = target
		a.revision++
		return Result{}, nil
	case *parser.SessionAuthorization:
		target := v.State.Authenticated
		if !s.Default {
			d, ok := v.Roles.Lookup(s.Name)
			if !ok {
				return Result{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", s.Name))
			}
			target = d.Ref
		}
		auth, _ := v.Roles.Descriptor(v.State.Authenticated)
		if !auth.Superuser && target != auth.Ref {
			return Result{}, sqlError("42501", fmt.Sprintf("permission denied to set session authorization \"%s\"", s.Name))
		}
		session := op.session
		settings := session.settings
		settings.mu.Lock()
		defer settings.mu.Unlock()
		session.mu.Lock()
		defer session.mu.Unlock()
		a := session.authorization
		if err := roleRevisionHeadroom(a, uint64(len(session.transactions.savepoints))+3); err != nil {
			return Result{}, err
		}
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		e.touchOperationIdentityLocked(op)
		settings.touchRole(a.state.CurrentUser, false)
		a.state.SessionUser = target
		a.state.CurrentUser = target
		a.revision++
		return Result{}, nil
	case *parser.DropRole:
		actor, exists := v.Roles.Find(v.State.CurrentUser)
		if !exists || !actor.Superuser {
			return Result{}, roleDiagnostic("42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
		}
		return e.dropRolesLocked(op, v, s)
	}
	return Result{}, fmt.Errorf("stuntgres: invalid role statement")
}

func relationDiagnosticName(auth AuthorizationView, relation *catalog.Relation) string {
	path, _ := auth.engine.referencePath(auth)
	return relationDiagnosticNameAt(auth.Catalog, relation, path)
}

func relationVisibleAt(catalog catalog.Read, relation *catalog.Relation, path []ids.ObjectAddress) bool {
	for _, ns := range path {
		if a, ok := catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: relation.Name()}); ok {
			return a == relation.Address()
		}
	}
	return false
}

func relationDiagnosticNameAt(catalogView catalog.Read, relation *catalog.Relation, path []ids.ObjectAddress) string {
	name := catalog.DependencyIdentifier(relation.Name())
	if relationVisibleAt(catalogView, relation, path) {
		return name
	}
	return catalog.DependencyIdentifier(catalogView.Get(relation.Namespace()).Name()) + "." + name
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
		kind = foreignLabel(foreign.Address().ClassID) + " "
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
		kind = relationDiagnosticKind(relation) + " "
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
func (e *Engine) dropRolesLocked(op *Operation, v AuthorizationView, s *parser.DropRole) (Result, error) {
	targets := make(map[authorization.PrincipalRef]authorization.RoleDescriptor, len(s.Names))
	ordered := make([]authorization.PrincipalRef, 0, len(s.Names))
	for _, name := range s.Names {
		d, ok := v.Roles.Lookup(name)
		if !ok {
			return Result{}, sqlError("42704", fmt.Sprintf("role \"%s\" does not exist", name))
		}
		if d.Ref == v.State.CurrentUser {
			return Result{}, sqlError("55006", "current user cannot be dropped")
		}
		if d.Ref == v.State.SessionUser {
			return Result{}, sqlError("55006", "session user cannot be dropped")
		}
		if _, ok := targets[d.Ref]; !ok {
			ordered = append(ordered, d.Ref)
			targets[d.Ref] = d
		}
	}
	// Hypothetical endpoint cleanup precedes every dependency check. A grantor
	// alone is not an endpoint and therefore retains its membership dependency.
	retained := v.RoleRecord.Root.RetainedMemberships(targets)
	for _, ref := range ordered {
		d := targets[ref]
		if d.Predefined() {
			return Result{}, roleDiagnostic("2BP01", "cannot drop role "+d.Name+" because it is required by the database system", "")
		}
		var dependencies []access.RoleDependency
		for _, object := range v.Catalog.Descriptors() {
			access.DescriptorRoleDependencies(object, ref, &dependencies)
		}
		access.DefaultRoleDependencies(v.Catalog, v.Roles, ref, &dependencies)
		for edge := range retained {
			if edge.Grantor == ref {
				member, _ := v.Roles.Descriptor(edge.Member)
				role, _ := v.Roles.Descriptor(edge.Role)
				dependencies = append(dependencies, access.RoleDependency{Kind: 'a', Description: "privileges for membership of role " + member.Name + " in role " + role.Name})
			}
		}
		for _, feature := range e.roles.referencingFeatures(ref) {
			dependencies = append(dependencies, access.RoleDependency{Kind: 'a', Description: "privileges for " + feature})
		}
		if len(dependencies) > 0 {
			sort.Slice(dependencies, func(i, j int) bool {
				a, b := dependencies[i], dependencies[j]
				if a.Address.ObjectID != b.Address.ObjectID {
					return a.Address.ObjectID < b.Address.ObjectID
				}
				if a.Address.ClassID != b.Address.ClassID {
					return a.Address.ClassID < b.Address.ClassID
				}
				if a.Address.SubID != b.Address.SubID {
					return uint32(a.Address.SubID) < uint32(b.Address.SubID)
				}
				if a.Kind != b.Kind {
					return a.Kind < b.Kind
				}
				return a.Description < b.Description
			})
			details := make([]string, len(dependencies))
			for i, dep := range dependencies {
				details[i] = dependencyDescription(v, dep)
			}
			return Result{}, roleDiagnostic("2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", d.Name), strings.Join(details, "\n"))
		}
	}
	if v.RoleRecord.Revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	n := v.RoleRecord.Root.Retire(targets)
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	e.installOperationRolesLocked(op, &access.RoleRecord{Root: n, Revision: v.RoleRecord.Revision + 1})
	if t := op.session.transactions; t.retainsTransaction() {
		// installOperationRolesLocked cloned the same catalog owner; no saved
		// candidate shares this mutable set. Standalone publication needs none.
		if t.catalog.retiredRoles == nil {
			t.catalog.retiredRoles = make(map[authorization.PrincipalRef]struct{}, len(ordered))
		}
		for _, ref := range ordered {
			t.catalog.retiredRoles[ref] = struct{}{}
		}
	}
	return Result{}, nil
}
