package schemacmds

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func labelError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

// Provider selection precedes target lookup and ownership. The target lock and
// immutable candidate fences survive private transaction/savepoint publication.
func ExecuteSecurityLabel(e *core.Engine, op *session.Operation, stmt *parser.SecurityLabel, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err == nil && (e.Sepgsql == nil || stmt.ProviderSpecified && stmt.Provider.Text != "selinux") {
		err = ddl.SecurityLabel(op.Context(), auth.View, stmt)
	}
	if err == nil {
		err = op.Context().Err()
	}
	var target ids.ObjectAddress
	if err == nil {
		target, err = resolveSecurityLabel(e, op, auth, stmt)
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	scope := core.ActiveScopeOfLease(op.Lease)
	if scope == nil {
		return core.Result{}, fmt.Errorf("stuntgres: security label without statement owner")
	}
	object := target
	object.SubID = 0
	mode := lock.AccessExclusive
	if object.ClassID == ids.RelationClassID {
		mode = lock.ShareUpdateExclusive
	} else if object.ClassID == ids.TypeClassID {
		mode = lock.TypeExclusive
	}
	if err = scope.AcquireCatalogResource(lock.Resource{ObjectRef: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), object)}, mode); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	current, err := e.AuthorizationLocked(op)
	if err == nil && current.Catalog.Get(object) != auth.Catalog.Get(object) {
		err = catalog.Conflict{}
	}
	if err == nil {
		err = checkSecurityLabelOwnership(op, current, target)
	}
	if err == nil && stmt.Kind == "column" {
		err = checkSecurityLabelColumn(current.Catalog, current.Catalog.MustRelation(object))
	}
	if err == nil && catalog.SepgsqlClass(current.Catalog.Get(object), target.SubID) == "" {
		err = labelError("0A000", "SELinux: security labels are not supported for this type of object")
	}
	var stage *core.CatalogStage
	if err == nil {
		stage = e.BeginCatalogStage(current.Catalog)
		stage.AuthorizeLocked(current, op.Session)
		err = core.StageSepgsqlRelabel(current, stage, target, stmt.Text, stmt.Null)
	}
	e.Mu.Unlock()
	if stage != nil {
		defer e.DiscardCatalogStage(stage)
	}
	if err != nil {
		return core.Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return core.Result{}, err
}
func resolveSecurityLabel(e *core.Engine, op *session.Operation, auth core.AuthorizationView, s *parser.SecurityLabel) (ids.ObjectAddress, error) {
	generic := false
	switch s.Kind {
	case "aggregate", "column", "database", "domain", "event trigger", "foreign table", "function", "language", "large object", "materialized view", "procedure", "publication", "role", "routine", "schema", "sequence", "subscription", "table", "tablespace", "type", "view":
		generic = true
	}
	if !generic {
		return ids.ObjectAddress{}, labelError("42809", "security labels are not supported for this type of object")
	}
	host := core.DDLHost{E: e, Op: op}
	var target ids.ObjectAddress
	switch s.Kind {
	case "table", "sequence", "view", "materialized view", "foreign table", "column":
		name := s.Name
		if s.Kind == "column" {
			name.Parts = name.Parts[:len(name.Parts)-1]
		}
		ref, err := host.ResolveRelation(auth, name)
		if err != nil {
			return target, err
		}
		target = ref.Address
		relation := auth.Catalog.MustRelation(target)
		expected := catalog.RelationKind(0)
		switch s.Kind {
		case "sequence":
			expected = catalog.SequenceRelation
		case "view":
			expected = catalog.ViewRelation
		case "materialized view":
			expected = catalog.MaterializedViewRelation
		case "foreign table":
			expected = catalog.ForeignRelation
		}
		if expected != 0 && relation.RelationKind != expected && !(expected == catalog.ViewRelation && relation.RelationKind == catalog.CatalogRelation && relation.CatalogView) {
			return target, labelError("42809", fmt.Sprintf(`"%s" is not a %s`, relation.Name(), s.Kind))
		}
		if s.Kind == "column" {
			column := s.Name.Parts[len(s.Name.Parts)-1].Text
			if index, ok := relation.Index[column]; ok {
				target.SubID = relation.Attributes[index]
			} else {
				for sub := int32(-1); sub >= -6; sub-- {
					if catalog.SepgsqlAttributeName(relation, sub) == column {
						target.SubID = sub
						break
					}
				}
				if target.SubID == 0 {
					return target, labelError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, relation.Name()))
				}
			}
		}
	case "function", "procedure", "routine", "aggregate":
		proc, _, err := routine.ResolveUtilityRoutine(RoutineHost{host}, auth, s.Kind, s.Routine)
		if err != nil {
			return target, err
		}
		target = proc.Address()
	case "schema", "database", "language", "tablespace":
		class := ids.NamespaceClassID
		switch s.Kind {
		case "database":
			class = ids.DatabaseClassID
		case "language":
			class = ids.LanguageClassID
		case "tablespace":
			class = ids.TablespaceClassID
		}
		target = auth.Catalog.NameAddress(ids.NameKey{ClassID: class, Name: s.Name.Parts[0].Text})
		if target.ObjectID == 0 {
			code := "42704"
			if s.Kind == "schema" {
				code = "3F000"
			}
			return target, labelError(code, fmt.Sprintf(`%s "%s" does not exist`, s.Kind, s.Name.Parts[0].Text))
		}
	case "publication", "event trigger", "subscription":
		class := ids.PublicationClassID
		if s.Kind == "event trigger" {
			class = ids.EventTriggerClassID
		} else if s.Kind == "subscription" {
			class = ids.SubscriptionClassID
		}
		key := ids.NameKey{ClassID: class, Name: s.Name.Parts[0].Text}
		if s.Kind == "subscription" {
			key = catalog.SubscriptionNameKey(auth.Catalog.Owner().DatabaseAddress(), s.Name.Parts[0].Text)
		}
		target = auth.Catalog.NameAddress(key)
		if target.ObjectID == 0 {
			return target, labelError("42704", fmt.Sprintf(`%s "%s" does not exist`, s.Kind, s.Name.Parts[0].Text))
		}
	case "large object":
		oidType, _ := auth.Catalog.LookupType(sqltype.Oid)
		value, err := oidType.Input(op.Context(), s.Number.Text, -1)
		if err != nil {
			return target, err
		}
		oid, err := value.OID()
		if err != nil {
			return target, err
		}
		target = ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: ids.OID(oid)}
		if auth.Catalog.Get(target) == nil {
			return target, core.LargeObjectMissingError(ids.OID(oid))
		}
	case "role":
		principal, ok := auth.Roles.Lookup(s.Name.Parts[0].Text)
		if !ok {
			return target, labelError("42704", fmt.Sprintf(`role "%s" does not exist`, s.Name.Parts[0].Text))
		}
		actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return target, err
		}
		if !actor.Superuser {
			if principal.Superuser {
				return target, role.Diagnostic("42501", "permission denied", "The current user must have the SUPERUSER attribute.")
			}
			if !actor.CreateRole {
				return target, role.Diagnostic("42501", "permission denied", "The current user must have the CREATEROLE attribute.")
			}
			admin, err := auth.Roles.AdminOfRole(actor.Ref, principal.Ref)
			if err != nil {
				return target, err
			}
			if !admin {
				return target, role.Diagnostic("42501", "permission denied", fmt.Sprintf(`The current user must have the ADMIN option on role "%s".`, principal.Name))
			}
		}
		return target, labelError("0A000", "SELinux: security labels are not supported for this type of object")
	case "type", "domain":
		id, err := (RoutineHost{host}).ResolveRegtype(auth, s.Type)
		if err != nil {
			return target, err
		}
		target = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}
		typ := auth.Catalog.MustType(target)
		if s.Kind == "domain" && !typ.SQLType.IsDomain() {
			return target, labelError("42809", fmt.Sprintf(`"%s" is not a domain`, securityLabelTypeName(s.Type)))
		}
	default:
		return target, labelError("0A000", "SELinux: security labels are not supported for this type of object")
	}
	return target, checkSecurityLabelOwnership(op, auth, target)
}

func checkSecurityLabelOwnership(op *session.Operation, auth core.AuthorizationView, target ids.ObjectAddress) error {
	if target.ClassID == ids.LargeObjectClassID {
		if compat, _ := op.Setting("lo_compat_privileges"); compat == "on" {
			return nil
		}
	}
	target.SubID = 0
	return auth.Check(access.Requirement{Object: auth.View.Ref(target), OwnerOnly: true})
}

// TypeNameToString uses decoded written qualification, not a selected regtype's
// display name. This is diagnostic spelling only; ResolveRegtype owns lookup.
func securityLabelTypeName(raw parser.TypeName) string {
	raw = parser.UtilityTypeName(raw)
	var name strings.Builder
	if raw.System {
		name.WriteString("pg_catalog.")
		name.WriteString(raw.Name)
	} else if len(raw.Parts) != 0 {
		for i, part := range raw.Parts {
			if i != 0 {
				name.WriteByte('.')
			}
			name.WriteString(part.Text)
		}
	} else {
		if raw.Schema != nil {
			name.WriteString(raw.Schema.Text)
			name.WriteByte('.')
		}
		name.WriteString(raw.Name)
	}
	if raw.PctType {
		name.WriteString("%TYPE")
	}
	if len(raw.ArrayBounds) != 0 {
		name.WriteString("[]")
	}
	return name.String()
}

// Generic COLUMN integrity follows fresh ownership and precedes the provider.
// CatalogRelation represents the engine's real SQL catalog tables and views.
func checkSecurityLabelColumn(read catalog.Read, relation *catalog.Relation) error {
	switch relation.RelationKind {
	case catalog.TableRelation, catalog.PartitionedRelation, catalog.ViewRelation,
		catalog.MaterializedViewRelation, catalog.CompositeRelation,
		catalog.ForeignRelation, catalog.CatalogRelation:
		return nil
	}
	detail := ""
	switch relation.RelationKind {
	case catalog.SequenceRelation:
		detail = "This operation is not supported for sequences."
	case catalog.IndexRelation:
		detail = "This operation is not supported for indexes."
		if relation.SQLIndex != nil {
			if parent, ok := read.Relation(relation.SQLIndex.Parent); ok && parent.Partition != nil {
				detail = "This operation is not supported for partitioned indexes."
			}
		}
	}
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809",
		Message: fmt.Sprintf(`cannot set security label on relation "%s"`, relation.Name()), Detail: detail,
	}
}
