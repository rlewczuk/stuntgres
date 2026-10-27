package access

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// View is the immutable captured authorization pair: a catalog read
// and the role read taken from the same publication, with the identities
// that decide privileges. It holds no engine or session pointer. The catalog
// owner stands in for the engine: a view rejects every reference and catalog
// that the owner does not back, and every stale epoch.
//
// The privilege checks are methods of View. The two inputs that belong
// to the session, the own TEMP namespace and the reference search path, come
// from an Environment at call time. The engine's AuthorizationView embeds
// a View and passes itself as that environment.
type View struct {
	Owner         *catalog.Owner
	Catalog       catalog.Read
	PairedCatalog catalog.Read
	RoleRecord    *RoleRecord
	Roles         authorization.Read
	// Committed predecessor captured under Engine.mu, distinct from private
	// database authority retained by catalog/roles.
	DatabasePredecessor catalog.Descriptor
	State               authorization.RoleState
	// Actual session identities remain distinct from trusted import authority.
	SessionRoleState authorization.RoleState
	Revision         uint64
	// Only command admission can retain an actually installed, unnamed role ID.
	// Arbitrary stale/retired actor substitutions do not acquire this authority.
	MissingCurrent authorization.PrincipalRef
}

// Environment supplies the session inputs of the privilege checks.
// Both are read lazily, at the moment a check needs them, and never captured
// as values: TemporaryNamespace returns the session's own TEMP namespace (or
// the zero address), and ReferencePath returns the search path used to
// qualify type names in diagnostics. Keep it exactly two methods wide.
type Environment interface {
	TemporaryNamespace() ids.ObjectAddress
	ReferencePath() ([]ids.ObjectAddress, error)
}

// Requirement names the privileges, or the ownership, a check demands on
// one object.
type Requirement struct {
	Object     ids.ObjectRef
	Privileges authorization.Privileges
	OwnerOnly  bool
	OwnerNoun  string // Utility diagnostic context; never changes authority.
}

// Stamp identifies a captured view: its catalog epoch, the role
// publication revision and the session authorization revision.
type Stamp struct {
	Catalog        ids.CatalogEpoch
	Roles, Session uint64
}

// Stamp returns the view's stamp, or the zero stamp without a role record.
func (v View) Stamp() Stamp {
	if v.RoleRecord == nil {
		return Stamp{}
	}
	return Stamp{v.Catalog.Epoch(), v.RoleRecord.Revision, v.Revision}
}

// Ref returns the reference to address in the view's catalog epoch.
func (v View) Ref(address ids.ObjectAddress) ids.ObjectRef {
	return v.Owner.Domain.Ref(v.Catalog.Epoch(), address)
}

// Recapture binds a substituted catalog to the same immutable role root.
func (v *View) Recapture(catalog catalog.Read) {
	v.Catalog = catalog
	v.PairedCatalog = catalog
	v.Roles = RoleRead(v.Owner, v.Owner.Principals, catalog, v.RoleRecord)
}

// DescriptorACL returns the owner, ACL and column ACLs that decide access to d.
func DescriptorACL(d catalog.Descriptor) (authorization.PrincipalRef, authorization.ACLValue, map[int32]authorization.ACLValue) {
	switch d := d.(type) {
	case *catalog.Database:
		return d.Owner, d.ACL, nil
	case *catalog.ForeignObject:
		if d.Address().ClassID == ids.ForeignMappingClassID {
			return d.MappedRole, authorization.ACLValue{}, nil
		}
		return d.Owner, d.ACL, nil
	case *catalog.Extension:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.Procedure:
		return d.Owner, d.ACL, nil
	case *catalog.Language:
		return d.Owner, d.ACL, nil
	case *catalog.EventTrigger:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.Parameter:
		return d.Owner, d.ACL, nil
	case *catalog.Tablespace:
		return d.Owner, d.ACL, nil
	case *catalog.LargeObject:
		return d.Owner, d.ACL, nil
	case *catalog.TSConfiguration:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.TSDictionary:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.Namespace:
		return d.Owner, d.ACL, nil
	case *catalog.Relation:
		return d.Owner, d.Security.ACL, d.Security.ColumnACL
	case *catalog.Type:
		return d.Owner, d.ACL, nil
	case *catalog.Collation:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.Conversion:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.Statistics:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.OperatorFamily:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.OperatorClass:
		return d.Owner, authorization.ACLValue{}, nil
	case *catalog.Operator:
		return d.Owner, authorization.ACLValue{}, nil
	}
	return authorization.PrincipalRef{}, authorization.ACLValue{}, nil
}

// ClassPrivileges returns the privileges that can be granted on d.
func ClassPrivileges(d catalog.Descriptor) authorization.Privileges {
	if d.Kind() == ids.LanguageObject {
		return authorization.UsagePrivilege
	}
	if d.Kind() == ids.LargeObjectObject {
		return authorization.SelectPrivilege | authorization.UpdatePrivilege
	}
	if d.Kind() == ids.ForeignWrapperObject || d.Kind() == ids.ForeignServerObject {
		return authorization.UsagePrivilege
	}
	if d.Kind() == ids.ForeignMappingObject {
		return 0
	}
	if d.Kind() == ids.DatabaseObject {
		return authorization.DatabasePrivileges
	}
	if d.Kind() == ids.ProcedureObject {
		return authorization.ExecutePrivilege
	}
	if d.Kind() == ids.ParameterObject {
		return authorization.SetPrivilege | authorization.AlterSystemPrivilege
	}
	if d.Kind() == ids.TablespaceObject {
		return authorization.CreatePrivilege
	}
	if d.Kind() == ids.StatisticsObject || d.Kind() == ids.CollationObject || d.Kind() == ids.ConversionObject {
		return 0
	}
	if d.Kind() == ids.ExtensionObject || d.Kind() == ids.TSDictionaryObject || d.Kind() == ids.TSTemplateObject || d.Kind() == ids.TSConfigurationObject {
		return 0
	}
	if d.Kind() == ids.TypeObject {
		return authorization.UsagePrivilege
	}
	if d.Kind() == ids.NamespaceObject {
		return authorization.UsagePrivilege | authorization.CreatePrivilege
	}
	if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.SequenceRelation {
		return authorization.SelectPrivilege | authorization.UsagePrivilege | authorization.UpdatePrivilege
	}
	return authorization.TableGrantPrivileges
}

// Object resolves ref in the view's catalog. It rejects references and
// views that the view's catalog owner does not back, stale epochs, unpaired
// role reads and descriptors whose owner is not a live principal.
func (v View) Object(ref ids.ObjectRef) (catalog.Descriptor, error) {
	if v.Owner == nil || v.Catalog.Owner() != v.Owner || v.Catalog.State() == nil || !v.Roles.BelongsTo(v.Owner.Principals) || v.Roles != RoleRead(v.Owner, v.Owner.Principals, v.Catalog, v.RoleRecord) || v.PairedCatalog != v.Catalog || v.Roles.DatabaseOwner() != v.Catalog.MustDatabase(v.Owner.DatabaseAddress()).Owner || ref.Domain() != v.Owner.Domain || ref.Epoch != v.Catalog.Epoch() {
		return nil, fmt.Errorf("stuntgres: foreign or stale authorization object")
	}
	d, ok := v.Catalog.Descriptor(ids.ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID})
	if !ok || ref.Address.SubID < 0 {
		return nil, fmt.Errorf("stuntgres: invalid authorization object")
	}
	if ref.Address.SubID > 0 {
		r, ok := d.(*catalog.Relation)
		if !ok || !r.HasAttribute(ref.Address.SubID) {
			return nil, fmt.Errorf("stuntgres: invalid authorization column")
		}
	}
	owner, _, _ := DescriptorACL(d)
	if _, err := v.Roles.Descriptor(owner); err != nil {
		return nil, err
	}
	return d, nil
}

// ownsTemporaryNamespace reports whether ns is the environment's own TEMP
// namespace. The environment is consulted only for a temporary namespace.
func ownsTemporaryNamespace(env Environment, ns *catalog.Namespace) bool {
	return ns != nil && !ns.TemporaryToast && ns.Temporary != (ids.SessionToken{}) && env.TemporaryNamespace() == ns.Address()
}

// Effective returns the privileges the current user holds on ref, or the
// grantable ones with grantOptions. env supplies the own TEMP namespace.
func (v View) Effective(env Environment, ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error) {
	d, err := v.Object(ref)
	if err != nil {
		return 0, err
	}
	if typ, ok := d.(*catalog.Type); ok {
		authority, err := catalog.TypeUsageAuthority(v.Catalog, typ)
		if err != nil {
			return 0, err
		}
		if authority != typ {
			return v.Effective(env, v.Ref(authority.Address()), grantOptions)
		}
	}
	owner, acl, columns := DescriptorACL(d)
	if err := v.Roles.ValidatePrincipal(v.State.CurrentUser); err != nil {
		return 0, err
	}
	actor, actorExists := v.Roles.Find(v.State.CurrentUser)
	if !actorExists && v.State.CurrentUser != v.MissingCurrent {
		return 0, fmt.Errorf("stuntgres: stale principal")
	}
	mask := ClassPrivileges(d)
	if ref.Address.SubID > 0 {
		mask = authorization.ColumnGrantPrivileges
	}
	if actorExists && actor.Superuser {
		return mask, nil
	}
	// PostgreSQL's own TEMP namespace follows the physical backend, not the
	// current role. Keep this ordinary namespace authority separate from object
	// ACLs, namespace ownership and delegation, and from private maintenance.
	if ns, ok := d.(*catalog.Namespace); ok && !grantOptions && ownsTemporaryNamespace(env, ns) {
		return v.OwnTemporaryPrivileges(env)
	}
	principals := []authorization.PrincipalRef{v.State.CurrentUser}
	if actorExists {
		principals, err = v.Roles.RoleClosure(context.Background(), actor.Ref, true)
		if err != nil {
			return 0, err
		}
	}
	if grantOptions {
		for _, ref := range principals {
			if ref == owner {
				return mask, nil
			}
		}
	}
	rights := authorization.ACLRights(acl, owner, principals, mask, grantOptions)
	if d.Kind() == ids.DatabaseObject && !acl.Explicit && !grantOptions {
		rights |= authorization.DatabasePublicPrivileges
	}
	if d.Kind() == ids.ProcedureObject && !acl.Explicit && !grantOptions {
		rights |= authorization.ExecutePrivilege
	}
	if d.Kind() == ids.TypeObject && !acl.Explicit && !grantOptions {
		rights |= authorization.UsagePrivilege
	}
	if language, ok := d.(*catalog.Language); ok && language.Trusted && !acl.Explicit && !grantOptions {
		rights |= authorization.UsagePrivilege
	}
	if ref.Address.SubID > 0 {
		rights |= authorization.ACLRights(columns[ref.Address.SubID], authorization.PrincipalRef{}, principals, mask, grantOptions)
	}
	if !grantOptions && actorExists {
		rights |= PredefinedPrivileges(d, principals)
	}
	return rights & mask, nil
}

// Own TEMP namespaces remain usable after revocation, but CREATE is contingent
// on the current effective role's database TEMP privilege, not authentication.
func (v View) OwnTemporaryPrivileges(env Environment) (authorization.Privileges, error) {
	rights, err := v.Effective(env, v.Ref(v.Owner.DatabaseAddress()), false)
	if err != nil {
		return 0, err
	}
	if rights&authorization.TemporaryPrivilege != 0 {
		return authorization.UsagePrivilege | authorization.CreatePrivilege, nil
	}
	return authorization.UsagePrivilege, nil
}

// Check enforces req for the current user. Denials carry PostgreSQL's
// SQLSTATE and message; env supplies the TEMP namespace and the type path.
func (v View) Check(env Environment, req Requirement) error {
	d, err := v.Object(req.Object)
	if err != nil {
		return err
	}
	if t, ok := d.(*catalog.Type); ok && t.SQLType.IsArray() && !req.OwnerOnly && req.Privileges == authorization.UsagePrivilege {
		req.Object.Address.ObjectID = ids.OID(t.SQLType.Element().ID())
		return v.Check(env, req)
	}
	if err := v.Roles.ValidatePrincipal(v.State.CurrentUser); err != nil {
		return err
	}
	if _, exists := v.Roles.Find(v.State.CurrentUser); !exists && v.State.CurrentUser != v.MissingCurrent {
		return fmt.Errorf("stuntgres: stale principal")
	}
	owner, _, _ := DescriptorACL(d)
	if req.OwnerOnly {
		if req.Privileges != 0 {
			return fmt.Errorf("stuntgres: owner check has ordinary mask")
		}
		allowed := v.State.CurrentUser == owner
		if _, exists := v.Roles.Find(v.State.CurrentUser); exists && !allowed {
			allowed, err = v.Roles.InheritsRole(v.State.CurrentUser, owner)
			if err != nil {
				return err
			}
		}
		if allowed {
			return nil
		}
		kind := "table"
		if t, ok := d.(*catalog.Type); ok {
			path, _ := env.ReferencePath()
			return sqlError("42501", "must be owner of type "+catalog.TypeDiagnosticNameAt(v.Catalog, nil, t, path))
		}
		if d.Kind() == ids.NamespaceObject {
			kind = "schema"
		} else if d.Kind() == ids.CollationObject {
			kind = "collation"
		} else if d.Kind() == ids.ConversionObject {
			kind = "conversion"
		} else if d.Kind() == ids.StatisticsObject {
			kind = "statistics object"
		} else if d.Kind() == ids.OperatorFamilyObject {
			kind = "operator family"
		} else if d.Kind() == ids.OperatorClassObject {
			kind = "operator class"
		} else if d.Kind() == ids.OperatorObject {
			kind = "operator"
		} else if d.Kind() == ids.EventTriggerObject {
			kind = "event trigger"
		} else if d.Kind() == ids.ProcedureObject {
			kind = "function"
		} else if d.Kind() == ids.DatabaseObject {
			kind = "database"
		} else if d.Kind() == ids.ForeignWrapperObject {
			kind = "foreign-data wrapper"
		} else if d.Kind() == ids.ForeignServerObject {
			kind = "foreign server"
		} else if d.Kind() == ids.LargeObjectObject {
			kind = "large object"
		} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.SequenceRelation {
			kind = "sequence"
		} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.ForeignRelation {
			kind = "foreign table"
		}
		if r, ok := d.(*catalog.Relation); ok && (r.RelationKind == catalog.ViewRelation || r.VirtualView) {
			kind = "view"
		}
		if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.MaterializedViewRelation {
			kind = "materialized view"
		}
		if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.IndexRelation {
			kind = "index"
		}
		if req.Object.Address.SubID > 0 && d.Kind() == ids.RelationObject {
			kind = "relation"
		}
		if req.OwnerNoun != "" {
			kind = req.OwnerNoun
		}
		name := d.Name()
		return sqlError("42501", fmt.Sprintf("must be owner of %s %s", kind, name))
	}
	mask := ClassPrivileges(d)
	if req.Object.Address.SubID > 0 {
		mask = authorization.ColumnGrantPrivileges
	}
	if req.Privileges == 0 || req.Privileges & ^mask != 0 {
		return fmt.Errorf("stuntgres: invalid privilege mask")
	}
	rights, err := v.Effective(env, req.Object, false)
	if err != nil {
		return err
	}
	if rights&req.Privileges == req.Privileges {
		return nil
	}
	kind := "table"
	if t, ok := d.(*catalog.Type); ok {
		if t.Namespace().ObjectID == ids.PGCatalogNamespaceOID && !t.SQLType.IsUserDefined() {
			return sqlError("42501", "permission denied for type "+t.SQLType.DiagnosticName())
		}
		path, _ := env.ReferencePath()
		return sqlError("42501", "permission denied for type "+catalog.TypeDiagnosticNameAt(v.Catalog, nil, t, path))
	}
	if d.Kind() == ids.NamespaceObject {
		kind = "schema"
	} else if d.Kind() == ids.DatabaseObject {
		kind = "database"
	} else if d.Kind() == ids.TablespaceObject {
		kind = "tablespace"
	} else if d.Kind() == ids.LanguageObject {
		kind = "language"
	} else if d.Kind() == ids.EventTriggerObject {
		kind = "event trigger"
	} else if d.Kind() == ids.ProcedureObject {
		kind = "function"
		if p, ok := d.(*catalog.Procedure); ok && p.RoutineKind == catalog.ProcedureRoutine {
			kind = "procedure"
		}
	} else if d.Kind() == ids.ForeignWrapperObject {
		kind = "foreign-data wrapper"
	} else if d.Kind() == ids.LargeObjectObject {
		kind = "large object"
	} else if d.Kind() == ids.ForeignServerObject {
		kind = "foreign server"
	} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.SequenceRelation {
		kind = "sequence"
	} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.ForeignRelation {
		kind = "foreign table"
	}
	if r, ok := d.(*catalog.Relation); ok && (r.RelationKind == catalog.ViewRelation || r.VirtualView) {
		kind = "view"
	}
	if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.MaterializedViewRelation {
		kind = "materialized view"
	}
	name := d.Name()
	if d.Kind() == ids.DatabaseObject {
		name = catalog.DependencyIdentifier(name)
	}
	return sqlError("42501", fmt.Sprintf("permission denied for %s %s", kind, name))
}

// CanUse checks USAGE on ref.
func (v View) CanUse(env Environment, ref ids.ObjectRef) error {
	return v.Check(env, Requirement{Object: ref, Privileges: authorization.UsagePrivilege})
}

// CheckCreate checks CREATE on ref.
func (v View) CheckCreate(env Environment, ref ids.ObjectRef) error {
	return v.Check(env, Requirement{Object: ref, Privileges: authorization.CreatePrivilege})
}

// CheckColumns checks mask on the columns of relation at the given dense
// column indices. With no indices, any live column, or the whole relation,
// can supply the privilege.
func (v View) CheckColumns(env Environment, ref ids.ObjectRef, relation *catalog.Relation, mask authorization.Privileges, indices []int) error {
	if relation.RelationKind == catalog.SequenceRelation && mask != authorization.SelectPrivilege {
		return v.Check(env, Requirement{Object: ref, Privileges: mask})
	}
	whole, err := v.Effective(env, ref, false)
	if err != nil {
		return err
	}
	if whole&mask == mask {
		return nil
	}
	if len(indices) == 0 {
		// An empty SELECT/INSERT manifest still needs authority, but any
		// live column can supply it. Attribute IDs survive dense-slot edits.
		for _, attribute := range relation.Attributes {
			column := ref
			column.Address.SubID = attribute
			rights, err := v.Effective(env, column, false)
			if err != nil {
				return err
			}
			if rights&mask == mask {
				return nil
			}
		}
		return v.Check(env, Requirement{Object: ref, Privileges: mask})
	}
	// The usual small relation needs no allocation to deduplicate actual uses.
	// Larger column-only ACL manifests use one bit per stable column position.
	var small uint64
	var seen []uint64
	if len(relation.Columns) > 64 {
		seen = make([]uint64, (len(relation.Columns)+63)/64)
	}
	check := func(index int) error {
		word := &small
		if seen != nil {
			word = &seen[index/64]
		}
		bit := uint64(1) << uint(index%64)
		if *word&bit != 0 {
			return nil
		}
		*word |= bit
		column := ref
		column.Address.SubID = relation.Attributes[index]
		return v.Check(env, Requirement{Object: column, Privileges: mask})
	}
	for _, index := range indices {
		if err := check(index); err != nil {
			return err
		}
	}
	return nil
}

// ValidateACL checks that every principal and ACL of d belongs to r.
func ValidateACL(r authorization.Read, d catalog.Descriptor) error {
	// Ownerless descriptors have no principal or role-owned ACL. Their
	// catalog providers validate structural authority and referenced objects.
	switch t := d.(type) {
	case *catalog.ForeignObject:
		if t.Address().ClassID == ids.ForeignMappingClassID {
			if t.MappedRole == (authorization.PrincipalRef{}) {
				return nil
			}
			return r.ValidatePrincipal(t.MappedRole)
		}
	case *catalog.AccessMethod, *catalog.Cast, *catalog.TSTemplate, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		return nil
	}
	owner, acl, cols := DescriptorACL(d)
	if _, err := r.Descriptor(owner); err != nil {
		return err
	}
	if relation, ok := d.(*catalog.Relation); ok {
		for _, policy := range relation.Security.Policies {
			for _, role := range policy.Roles {
				if role.Public {
					if role.Ref != (authorization.PrincipalRef{}) {
						return fmt.Errorf("stuntgres: PUBLIC policy principal")
					}
				} else if _, err := r.Descriptor(role.Ref); err != nil {
					return err
				}
			}
		}
	}
	if err := authorization.ValidateACL(r, acl, ClassPrivileges(d)); err != nil {
		return err
	}
	for sub, a := range cols {
		relation, ok := d.(*catalog.Relation)
		if !ok || sub <= 0 || !relation.HasAttribute(sub) {
			return fmt.Errorf("stuntgres: invalid ACL column")
		}
		if err := authorization.ValidateACL(r, a, authorization.ColumnGrantPrivileges); err != nil {
			return err
		}
	}
	return nil
}
