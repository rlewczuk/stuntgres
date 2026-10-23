// Package role decides role and ACL DDL: GRANT/REVOKE, ALTER DEFAULT
// PRIVILEGES, ALTER ... OWNER and REASSIGN/DROP OWNED plans and staging, and
// the role diagnostics. The engine captures authority and publishes.
package role

import (
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

// EmitPasswordNotice emits the NOTICE or WARNING a password option raises.
func EmitPasswordNotice(op *session.Operation, n authorization.PasswordNotice) {
	switch n {
	case authorization.PasswordEmptyNotice:
		op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"})
	case authorization.PasswordMD5Notice:
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."})
	}
}

// Diagnostic is a role DDL ERROR with a DETAIL line (none when detail is
// empty).
func Diagnostic(code, message, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
}

// ConnectionLimitError is the FATAL error of a role at its connection limit.
func ConnectionLimitError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: "too many connections for role \"" + name + "\""}
}

// LoginError is the FATAL error of a role without LOGIN.
func LoginError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: "role \"" + name + "\" is not permitted to log in"}
}

// DependencyDescription describes a role dependency the way PostgreSQL's
// DROP ROLE and DROP OWNED details do: ownership, privileges or a policy
// target, then the object. path qualifies names not visible on it.
func DependencyDescription(v access.View, path []ids.ObjectAddress, dep access.RoleDependency) string {
	read := v.Catalog
	if dep.Kind == 'r' {
		relation := read.MustRelation(dep.PolicyTable)
		return "target of policy " + catalog.DependencyIdentifier(dep.PolicyName) + " on table " + stored.RelationDiagnosticNameAt(read, relation, path)
	}
	if dep.Description != "" {
		return dep.Description
	}
	address := dep.Address
	address.SubID = 0
	d := read.Get(address)
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
	case *catalog.Language:
		kind = "language "
	case *catalog.LargeObject:
		kind, name = "large object ", d.Name()
	case *catalog.Extension:
		kind = "extension "
	case *catalog.Procedure:
		kind = "function "
	case *catalog.TSDictionary:
		kind = "text search dictionary "
	}
	if foreign, ok := d.(*catalog.ForeignObject); ok {
		kind = catalog.ForeignLabel(foreign.Address().ClassID) + " "
		if foreign.Address().ClassID == ids.ForeignMappingClassID {
			kind = "user mapping for "
			mapped, _ := v.Roles.Descriptor(foreign.MappedRole)
			name = catalog.DependencyIdentifier(mapped.Name) + " on server " + catalog.DependencyIdentifier(read.Get(foreign.Server).Name())
		}
	}
	if d.Kind() == ids.ProcedureObject || d.Kind() == ids.TSDictionaryObject {
		name = strings.TrimPrefix(stored.DependencyObject(read, d.Address(), path), kind)
	}
	if relation, ok := d.(*catalog.Relation); ok {
		kind = catalog.RelationDiagnosticKind(relation) + " "
		name = stored.RelationDiagnosticNameAt(read, relation, path)
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
			typ := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
			kind, name = "type ", catalog.TypeDiagnosticNameAt(read, typ, path)
		}
	}
	if typ, ok := d.(*catalog.Type); ok && (typ.SQLType.IsUserDefined() || typ.SQLType.IsArray() && typ.SQLType.Element().IsDomain()) {
		kind = "type "
		name = catalog.TypeDiagnosticNameAt(read, typ, path)
	}
	if c, ok := d.(*catalog.Collation); ok {
		kind = "collation "
		name = strings.TrimPrefix(stored.DependencyObject(read, c.Address(), path), "collation ")
	}
	if c, ok := d.(*catalog.Conversion); ok {
		kind = "conversion "
		name = strings.TrimPrefix(stored.DependencyObject(read, c.Address(), path), "conversion ")
	}
	prefix := "privileges for "
	if dep.Kind == 'o' {
		prefix = "owner of "
	}
	return prefix + kind + name
}
