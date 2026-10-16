package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The role default is an internal physical-session hook, not a registered GUC
// or a public connector option. Providers run without publication locks.
func (e *Engine) initialRole(ctx context.Context, authenticated authorization.PrincipalRef, provider SessionDefaultProvider) (authorization.PrincipalRef, error) {
	if provider == nil {
		return authorization.PrincipalRef{}, nil
	}
	name, present, err := provider.SessionDefault(ctx, SettingDefinition{Name: "role", BootDefault: "none", Type: SettingString})
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
	roles := e.roleRead(CatalogRead{e, e.state}, e.roles)
	target, ok := roles.Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", name))
	}
	if err := validateInitialRole(roles, authenticated, target.Ref); err != nil {
		return authorization.PrincipalRef{}, err
	}
	return target.Ref, nil
}

func validateInitialRole(roles authorization.Read, authenticated, target authorization.PrincipalRef) error {
	allowed, err := roles.CanSetRole(authenticated, target)
	if err != nil {
		return err
	}
	if !allowed {
		role, _ := roles.Descriptor(target)
		return sqlError("42501", fmt.Sprintf("permission denied to set role \"%s\"", role.Name))
	}
	return nil
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
		target := v.state.SessionUser
		if s.Reset && v.state.ResetRole != (authorization.PrincipalRef{}) {
			target = v.state.ResetRole
		} else if !s.Reset && s.Name != "none" {
			d, ok := v.roles.Lookup(s.Name)
			if !ok {
				return Result{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", s.Name))
			}
			target = d.Ref
		}
		if s.Reset {
			if _, err := v.roles.Descriptor(target); err != nil {
				return Result{}, err
			}
		} else {
			allowed := target == v.state.SessionUser
			if !allowed {
				allowed, err = v.roles.CanSetRole(v.state.SessionUser, target)
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
		target := v.state.Authenticated
		if !s.Default {
			d, ok := v.roles.Lookup(s.Name)
			if !ok {
				return Result{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", s.Name))
			}
			target = d.Ref
		}
		auth, _ := v.roles.Descriptor(v.state.Authenticated)
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
		actor, exists := v.roles.Find(v.state.CurrentUser)
		if !exists || !actor.Superuser {
			return Result{}, roleDiagnostic("42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
		}
		return e.dropRolesLocked(op, v, s)
	}
	return Result{}, fmt.Errorf("stuntgres: invalid role statement")
}

type roleDependency struct {
	address     ObjectAddress
	kind        byte // PostgreSQL shared dependency types: ACL ('a'), owner ('o').
	description string
	policyTable ObjectAddress
	policyName  string
}

// A nil collector stops at the first dependency without building diagnostics.
// Admission and final publication share the exact same descriptor classification.
func descriptorRoleDependencies(d Descriptor, ref authorization.PrincipalRef, deps *[]roleDependency) bool {
	if relation, ok := d.(*relationDescriptor); ok {
		for _, policy := range relation.policies {
			for _, role := range policy.roles {
				if !role.public && role.ref == ref {
					if recordRoleDependency(deps, roleDependency{address: ObjectAddress{ClassID: PolicyClassID, ObjectID: policy.OID}, kind: 'r', policyTable: relation.address, policyName: policy.Name}) {
						return true
					}
				}
			}
		}
	}
	if typ, ok := d.(*typeDescriptor); ok && typ.owningRelation != (ObjectAddress{}) {
		// Dependent row/array ownership belongs to the relation; independent
		// composite types still carry their own named ACL dependency.
		if !typ.typ.IsComposite() {
			return false
		}
		if typ.owner == ref {
			return false
		}
	}
	if relation, ok := d.(*relationDescriptor); ok && relation.relationKind == indexRelation {
		// Index ownership belongs to its validated parent relation.
		return false
	}
	owner, acl, columns := descriptorACL(d)
	if owner == ref {
		if typ, array := d.(*typeDescriptor); array && typ.typ.IsArray() {
			// Associated array ownership is represented by its parent.
			return false
		}
		// The owner's shared ACL dependencies are represented by ownership.
		return recordRoleDependency(deps, roleDependency{address: d.Address(), kind: 'o'})
	}
	if aclRoleDependencies(d.Address(), acl, ref, deps) {
		return true
	}
	for sub, acl := range columns {
		address := d.Address()
		address.SubID = sub
		if aclRoleDependencies(address, acl, ref, deps) {
			return true
		}
	}
	return false
}

func aclRoleDependencies(address ObjectAddress, acl authorization.ACLValue, ref authorization.PrincipalRef, deps *[]roleDependency) bool {
	for _, entry := range acl.Entries {
		if entry.Grantor == ref || !entry.Grantee.Public && entry.Grantee.Role == ref {
			return recordRoleDependency(deps, roleDependency{address: address, kind: 'a'})
		}
	}
	return false
}

func recordRoleDependency(deps *[]roleDependency, dependency roleDependency) bool {
	if deps == nil {
		return true
	}
	*deps = append(*deps, dependency)
	return false
}

// PostgreSQL 18 quote_identifier quotes every keyword except UNRESERVED.
// Keep this independent of the intentionally much smaller supported grammar.
func dependencyIdentifier(name string) string {
	safe := name != ""
	for i := range len(name) {
		c := name[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			safe = false
			break
		}
	}
	if !safe {
		return quoteIdentifier(name)
	}
	switch name {
	case "all", "analyse", "analyze", "and", "any", "array", "as", "asc", "asymmetric",
		"authorization", "between", "bigint", "binary", "bit", "boolean", "both",
		"case", "cast", "char", "character", "check", "coalesce", "collate", "collation",
		"column", "concurrently", "constraint", "create", "cross", "current_catalog",
		"current_date", "current_role", "current_schema", "current_time",
		"current_timestamp", "current_user", "dec", "decimal", "default", "deferrable",
		"desc", "distinct", "do", "else", "end", "except", "exists", "extract", "false",
		"fetch", "float", "for", "foreign", "freeze", "from", "full", "grant", "greatest",
		"group", "grouping", "having", "ilike", "in", "initially", "inner", "inout",
		"int", "integer", "intersect", "interval", "into", "is", "isnull", "join",
		"json", "json_array", "json_arrayagg", "json_exists", "json_object",
		"json_objectagg", "json_query", "json_scalar", "json_serialize", "json_table",
		"json_value", "lateral", "leading", "least", "left", "like", "limit",
		"localtime", "localtimestamp", "merge_action", "national", "natural", "nchar",
		"none", "normalize", "not", "notnull", "null", "nullif", "numeric", "offset",
		"on", "only", "or", "order", "out", "outer", "overlaps", "overlay", "placing",
		"position", "precision", "primary", "real", "references", "returning", "right",
		"row", "select", "session_user", "setof", "similar", "smallint", "some",
		"substring", "symmetric", "system_user", "table", "tablesample", "then", "time",
		"timestamp", "to", "trailing", "treat", "trim", "true", "union", "unique",
		"user", "using", "values", "varchar", "variadic", "verbose", "when", "where",
		"window", "with", "xmlattributes", "xmlconcat", "xmlelement", "xmlexists",
		"xmlforest", "xmlnamespaces", "xmlparse", "xmlpi", "xmlroot", "xmlserialize", "xmltable":
		return quoteIdentifier(name)
	default:
		return name
	}
}

func relationDiagnosticName(auth AuthorizationView, relation *relationDescriptor) string {
	path, _ := auth.engine.referencePath(auth)
	return relationDiagnosticNameAt(auth.catalog, relation, path)
}

func relationVisibleAt(catalog CatalogRead, relation *relationDescriptor, path []ObjectAddress) bool {
	for _, ns := range path {
		if a, ok := catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: relation.name}]; ok {
			return a == relation.address
		}
	}
	return false
}

func relationDiagnosticNameAt(catalog CatalogRead, relation *relationDescriptor, path []ObjectAddress) string {
	name := dependencyIdentifier(relation.name)
	if relationVisibleAt(catalog, relation, path) {
		return name
	}
	return dependencyIdentifier(catalog.state.descriptors[relation.namespace].Name()) + "." + name
}

func dependencyDescription(auth AuthorizationView, dep roleDependency) string {
	view := auth.catalog
	if dep.kind == 'r' {
		relation := view.state.descriptors[dep.policyTable].(*relationDescriptor)
		return "target of policy " + dependencyIdentifier(dep.policyName) + " on table " + relationDiagnosticName(auth, relation)
	}
	if dep.description != "" {
		return dep.description
	}
	address := dep.address
	address.SubID = 0
	d := view.state.descriptors[address]
	name := dependencyIdentifier(d.Name())
	if _, ok := d.(*namespaceDescriptor); ok {
		name = d.Name()
	}
	kind := "schema "
	switch d.(type) {
	case *databaseDescriptor:
		kind = "database "
	case *parameterDescriptor:
		kind = "parameter "
		name = d.Name()
	case *tablespaceDescriptor:
		kind = "tablespace "
	case *extensionDescriptor:
		kind = "extension "
	case *procedureDescriptor:
		kind = "function "
	case *tsDictionaryDescriptor:
		kind = "text search dictionary "
	}
	if foreign, ok := d.(*foreignDescriptor); ok {
		kind = foreignLabel(foreign.address.ClassID) + " "
		if foreign.address.ClassID == ForeignMappingClassID {
			kind = "user mapping for "
			mapped, _ := auth.roles.Descriptor(foreign.mappedRole)
			name = dependencyIdentifier(mapped.Name) + " on server " + dependencyIdentifier(view.state.descriptors[foreign.server].Name())
		}
	}
	if d.Kind() == ProcedureObject || d.Kind() == TSDictionaryObject {
		path, _ := auth.engine.referencePath(auth)
		name = strings.TrimPrefix(dependencyObject(view, d.Address(), path), kind)
	}
	if relation, ok := d.(*relationDescriptor); ok {
		kind = relationDiagnosticKind(relation) + " "
		name = relationDiagnosticName(auth, relation)
		if dep.address.SubID != 0 {
			// getObjectDescription interpolates the raw attribute name; only
			// getRelationDescription applies diagnostic identifier quoting.
			for slot, attribute := range relation.attributes {
				if attribute == dep.address.SubID {
					kind = "column " + relation.columns[slot].Name + " of table "
					break
				}
			}
		}
		if relation.relationKind == compositeRelation {
			path, _ := auth.engine.referencePath(auth)
			typ := view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}].(*typeDescriptor)
			kind, name = "type ", typeDiagnosticNameAt(view, typ, path)
		}
	}
	if typ, ok := d.(*typeDescriptor); ok && (typ.typ.IsUserDefined() || typ.typ.IsArray() && typ.typ.Element().IsDomain()) {
		path, _ := auth.engine.referencePath(auth)
		kind = "type "
		name = typeDiagnosticNameAt(view, typ, path)
	}
	if c, ok := d.(*collationDescriptor); ok {
		path, _ := auth.engine.referencePath(auth)
		kind = "collation "
		name = strings.TrimPrefix(dependencyObject(view, c.Address(), path), "collation ")
	}
	if c, ok := d.(*conversionDescriptor); ok {
		path, _ := auth.engine.referencePath(auth)
		kind = "conversion "
		name = strings.TrimPrefix(dependencyObject(view, c.Address(), path), "conversion ")
	}
	prefix := "privileges for "
	if dep.kind == 'o' {
		prefix = "owner of "
	}
	return prefix + kind + name
}
func (e *Engine) dropRolesLocked(op *Operation, v AuthorizationView, s *parser.DropRole) (Result, error) {
	targets := make(map[authorization.PrincipalRef]authorization.RoleDescriptor, len(s.Names))
	ordered := make([]authorization.PrincipalRef, 0, len(s.Names))
	for _, name := range s.Names {
		d, ok := v.roles.Lookup(name)
		if !ok {
			return Result{}, sqlError("42704", fmt.Sprintf("role \"%s\" does not exist", name))
		}
		if d.Ref == v.state.CurrentUser {
			return Result{}, sqlError("55006", "current user cannot be dropped")
		}
		if d.Ref == v.state.SessionUser {
			return Result{}, sqlError("55006", "session user cannot be dropped")
		}
		if _, ok := targets[d.Ref]; !ok {
			ordered = append(ordered, d.Ref)
			targets[d.Ref] = d
		}
	}
	// Hypothetical endpoint cleanup precedes every dependency check. A grantor
	// alone is not an endpoint and therefore retains its membership dependency.
	retained := v.roleRecord.root.RetainedMemberships(targets)
	for _, ref := range ordered {
		d := targets[ref]
		if d.Predefined() {
			return Result{}, roleDiagnostic("2BP01", "cannot drop role "+d.Name+" because it is required by the database system", "")
		}
		var dependencies []roleDependency
		for _, object := range v.catalog.state.descriptors {
			descriptorRoleDependencies(object, ref, &dependencies)
		}
		defaultRoleDependencies(v.catalog, v.roles, ref, &dependencies)
		for edge := range retained {
			if edge.Grantor == ref {
				member, _ := v.roles.Descriptor(edge.Member)
				role, _ := v.roles.Descriptor(edge.Role)
				dependencies = append(dependencies, roleDependency{kind: 'a', description: "privileges for membership of role " + member.Name + " in role " + role.Name})
			}
		}
		for feature, refs := range e.roleReferences {
			for _, dependency := range refs {
				if dependency == ref {
					dependencies = append(dependencies, roleDependency{kind: 'a', description: "privileges for " + feature})
					break
				}
			}
		}
		if len(dependencies) > 0 {
			sort.Slice(dependencies, func(i, j int) bool {
				a, b := dependencies[i], dependencies[j]
				if a.address.ObjectID != b.address.ObjectID {
					return a.address.ObjectID < b.address.ObjectID
				}
				if a.address.ClassID != b.address.ClassID {
					return a.address.ClassID < b.address.ClassID
				}
				if a.address.SubID != b.address.SubID {
					return uint32(a.address.SubID) < uint32(b.address.SubID)
				}
				if a.kind != b.kind {
					return a.kind < b.kind
				}
				return a.description < b.description
			})
			details := make([]string, len(dependencies))
			for i, dep := range dependencies {
				details[i] = dependencyDescription(v, dep)
			}
			return Result{}, roleDiagnostic("2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", d.Name), strings.Join(details, "\n"))
		}
	}
	if v.roleRecord.revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	n := v.roleRecord.root.Retire(targets)
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	e.installOperationRolesLocked(op, &roleCatalog{root: n, revision: v.roleRecord.revision + 1})
	if t := op.session.transactions; t.retainedMessage {
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
