package engine

import (
	"fmt"
	"sort"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func roleDiagnostic(code, message, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
}
func (e *Engine) executeRole(op *Operation, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
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
	actor, _ := v.roles.Descriptor(v.state.CurrentUser)
	switch s := stmt.(type) {
	case *parser.IdentitySelect:
		typ, _ := v.catalog.LookupType(sqltype.Name)
		columns := make([]ColumnDescriptor, len(s.Names))
		row := make([]sqltype.Value, len(s.Names))
		projection := make([]int, len(s.Names))
		for i, name := range s.Names {
			var value sqltype.Value
			if name == "current_schema" {
				path, _, err := e.explicitPath(v)
				if err != nil {
					return Result{}, err
				}
				value = sqltype.Null(typ)
				if len(path) > 0 {
					value = sqltype.String(typ, v.catalog.state.descriptors[path[0]].Name())
				}
			} else {
				ref := v.state.CurrentUser
				if name == "session_user" {
					ref = v.state.SessionUser
				}
				d, err := v.roles.Descriptor(ref)
				if err != nil {
					return Result{}, err
				}
				value = sqltype.String(typ, d.Name)
			}
			row[i] = value
			projection[i] = i
			columns[i] = ColumnDescriptor{Name: name, Usage: typ.Usage(), Nullable: name == "current_schema", NullabilityKnown: name == "current_schema"}
		}
		return Result{Columns: columns, Rows: anonymousSnapshot([][]sqltype.Value{row}, projection), Count: 1}, nil
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
		session.mu.Lock()
		defer session.mu.Unlock()
		a := session.authorization
		if a.revision == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: session authorization revision exhausted")
		}
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		a.state.SessionUser = target
		a.state.CurrentUser = target
		a.revision++
		return Result{}, nil
	case *parser.CreateRole:
		if s.OptionCount > 1 {
			return Result{}, insertError("42601", "conflicting or redundant options", s.SecondOption)
		}
		if !actor.Superuser {
			return Result{}, roleDiagnostic("42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles.")
		}
		name := s.Name.Text
		if name == "public" || name == "none" {
			return Result{}, sqlError("42939", fmt.Sprintf("role name \"%s\" is reserved", name))
		}
		if strings.HasPrefix(name, "pg_") {
			return Result{}, roleDiagnostic("42939", fmt.Sprintf("role name \"%s\" is reserved", name), "Role names starting with \"pg_\" are reserved.")
		}
		if _, ok := v.roles.Lookup(name); ok {
			return Result{}, sqlError("42710", fmt.Sprintf("role \"%s\" already exists", name))
		}
		if e.nextPrincipal == 0 || e.nextPrincipal == ^uint64(0) || e.roles.revision == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: principal identity or authorization revision exhausted")
		}
		ref := PrincipalRef{e, e.nextPrincipal}
		n := cloneRoles(e.roles)
		n.names[name] = ref
		n.roles[ref] = RoleDescriptor{Ref: ref, Name: name, Login: s.Login}
		op.session.mu.Lock()
		defer op.session.mu.Unlock()
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		e.nextPrincipal++
		e.roles = n
		return Result{}, nil
	case *parser.DropRole:
		if !actor.Superuser {
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
}

func descriptorRoleDependencies(deps []roleDependency, d Descriptor, ref PrincipalRef) []roleDependency {
	owner, acl, columns := descriptorACL(d)
	if owner == ref {
		deps = append(deps, roleDependency{address: d.Address(), kind: 'o'})
		// Shared ACL dependencies omit the owner, whose ownership dependency
		// already prevents removal, including when named by a column ACL.
		return deps
	}
	add := func(a ACLValue, sub int32) {
		for _, entry := range a.Entries {
			if entry.Grantor == ref || !entry.Grantee.Public && entry.Grantee.Role == ref {
				address := d.Address()
				address.SubID = sub
				deps = append(deps, roleDependency{address: address, kind: 'a'})
				return
			}
		}
	}
	add(acl, 0)
	for sub, acl := range columns {
		add(acl, sub)
	}
	return deps
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
	name := dependencyIdentifier(relation.name)
	path, err := auth.engine.referencePath(auth)
	if err == nil {
		for _, ns := range path {
			if a, ok := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: relation.name}]; ok {
				if a == relation.address {
					return name
				}
				break
			}
		}
	}
	return dependencyIdentifier(auth.catalog.state.descriptors[relation.namespace].Name()) + "." + name
}

func dependencyDescription(auth AuthorizationView, dep roleDependency) string {
	view := auth.catalog
	if dep.description != "" {
		return dep.description
	}
	address := dep.address
	address.SubID = 0
	d := view.state.descriptors[address]
	name := dependencyIdentifier(d.Name())
	kind := "schema "
	if relation, ok := d.(*relationDescriptor); ok {
		kind = "table "
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
	}
	prefix := "privileges for "
	if dep.kind == 'o' {
		prefix = "owner of "
	}
	return prefix + kind + name
}
func (e *Engine) dropRolesLocked(op *Operation, v AuthorizationView, s *parser.DropRole) (Result, error) {
	targets := make(map[PrincipalRef]RoleDescriptor, len(s.Names))
	ordered := make([]PrincipalRef, 0, len(s.Names))
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
	retained := make([]Membership, 0, len(e.roles.edges))
	for _, edge := range e.roles.edges {
		if _, ok := targets[edge.Role]; ok {
			continue
		}
		if _, ok := targets[edge.Member]; ok {
			continue
		}
		retained = append(retained, edge)
	}
	for _, ref := range ordered {
		d := targets[ref]
		var dependencies []roleDependency
		for _, object := range e.state.descriptors {
			dependencies = descriptorRoleDependencies(dependencies, object, ref)
		}
		for _, edge := range retained {
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
	if e.roles.revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	n := cloneRoles(e.roles)
	n.edges = retained
	for ref, d := range targets {
		delete(n.roles, ref)
		delete(n.names, d.Name)
	}
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	e.roles = n
	return Result{}, nil
}
