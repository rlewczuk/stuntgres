package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// accessView is the immutable captured authorization pair: a catalog read
// and the role read taken from the same publication, with the identities
// that decide privileges. It holds no engine or session pointer. The catalog
// owner stands in for the engine: a view rejects every reference and catalog
// that the owner does not back, and every stale epoch.
//
// The privilege checks are methods of accessView. The two inputs that belong
// to the session, the own TEMP namespace and the reference search path, come
// from an accessEnvironment at call time. AuthorizationView embeds an
// accessView and passes itself as that environment.
type accessView struct {
	owner         *catalog.Owner
	catalog       catalog.Read
	pairedCatalog catalog.Read
	roleRecord    *roleCatalog
	roles         authorization.Read
	// Committed predecessor captured under Engine.mu, distinct from private
	// database authority retained by catalog/roles.
	databasePredecessor catalog.Descriptor
	state               authorization.RoleState
	// Actual session identities remain distinct from trusted import authority.
	sessionRoleState authorization.RoleState
	revision         uint64
	// Only command admission can retain an actually installed, unnamed role ID.
	// Arbitrary stale/retired actor substitutions do not acquire this authority.
	missingCurrent authorization.PrincipalRef
}

// accessEnvironment supplies the session inputs of the privilege checks.
// Both are read lazily, at the moment a check needs them, and never captured
// as values: TemporaryNamespace returns the session's own TEMP namespace (or
// the zero address), and ReferencePath returns the search path used to
// qualify type names in diagnostics. Keep it exactly two methods wide.
type accessEnvironment interface {
	TemporaryNamespace() ids.ObjectAddress
	ReferencePath() ([]ids.ObjectAddress, error)
}

type AccessRequirement struct {
	Object     ids.ObjectRef
	Privileges authorization.Privileges
	OwnerOnly  bool
	OwnerNoun  string // Utility diagnostic context; never changes authority.
}
type AuthorizationStamp struct {
	Catalog        ids.CatalogEpoch
	Roles, Session uint64
}

func (v accessView) Roles() authorization.Read { return v.roles }
func (v accessView) Stamp() AuthorizationStamp {
	if v.roleRecord == nil {
		return AuthorizationStamp{}
	}
	return AuthorizationStamp{v.catalog.Epoch(), v.roleRecord.revision, v.revision}
}

// Ref returns the reference to address in the view's catalog epoch.
func (v accessView) Ref(address ids.ObjectAddress) ids.ObjectRef {
	return v.owner.Domain.Ref(v.catalog.Epoch(), address)
}

// Recapture binds a substituted catalog to the same immutable role root.
func (v *accessView) Recapture(catalog catalog.Read) {
	v.catalog = catalog
	v.pairedCatalog = catalog
	v.roles = roleRead(v.owner, v.owner.Principals, catalog, v.roleRecord)
}

func descriptorACL(d catalog.Descriptor) (authorization.PrincipalRef, authorization.ACLValue, map[int32]authorization.ACLValue) {
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
	case *catalog.Parameter:
		return d.Owner, d.ACL, nil
	case *catalog.Tablespace:
		return d.Owner, d.ACL, nil
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
	case *catalog.Operator:
		return d.Owner, authorization.ACLValue{}, nil
	}
	return authorization.PrincipalRef{}, authorization.ACLValue{}, nil
}
func classPrivileges(d catalog.Descriptor) authorization.Privileges {
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
	if d.Kind() == ids.CollationObject || d.Kind() == ids.ConversionObject {
		return 0
	}
	if d.Kind() == ids.ExtensionObject || d.Kind() == ids.TSDictionaryObject || d.Kind() == ids.TSTemplateObject {
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
func (v accessView) object(ref ids.ObjectRef) (catalog.Descriptor, error) {
	if v.owner == nil || v.catalog.Owner() != v.owner || v.catalog.State() == nil || !v.roles.BelongsTo(v.owner.Principals) || v.roles != roleRead(v.owner, v.owner.Principals, v.catalog, v.roleRecord) || v.pairedCatalog != v.catalog || v.roles.DatabaseOwner() != v.catalog.MustDatabase(catalog.CurrentDatabaseAddress).Owner || ref.Domain() != v.owner.Domain || ref.Epoch != v.catalog.Epoch() {
		return nil, fmt.Errorf("stuntgres: foreign or stale authorization object")
	}
	d, ok := v.catalog.Descriptor(ids.ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID})
	if !ok || ref.Address.SubID < 0 {
		return nil, fmt.Errorf("stuntgres: invalid authorization object")
	}
	if ref.Address.SubID > 0 {
		r, ok := d.(*catalog.Relation)
		if !ok || !r.HasAttribute(ref.Address.SubID) {
			return nil, fmt.Errorf("stuntgres: invalid authorization column")
		}
	}
	owner, _, _ := descriptorACL(d)
	if _, err := v.roles.Descriptor(owner); err != nil {
		return nil, err
	}
	return d, nil
}

// ownsTemporaryNamespace reports whether ns is the environment's own TEMP
// namespace. The environment is consulted only for a temporary namespace.
func ownsTemporaryNamespace(env accessEnvironment, ns *catalog.Namespace) bool {
	return ns != nil && !ns.TemporaryToast && ns.Temporary != (ids.SessionToken{}) && env.TemporaryNamespace() == ns.Address()
}

func (v accessView) Effective(env accessEnvironment, ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error) {
	d, err := v.object(ref)
	if err != nil {
		return 0, err
	}
	owner, acl, columns := descriptorACL(d)
	if err := v.roles.ValidatePrincipal(v.state.CurrentUser); err != nil {
		return 0, err
	}
	actor, actorExists := v.roles.Find(v.state.CurrentUser)
	if !actorExists && v.state.CurrentUser != v.missingCurrent {
		return 0, fmt.Errorf("stuntgres: stale principal")
	}
	mask := classPrivileges(d)
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
		return v.ownTemporaryPrivileges(env)
	}
	principals := []authorization.PrincipalRef{v.state.CurrentUser}
	if actorExists {
		principals, err = v.roles.RoleClosure(context.Background(), actor.Ref, true)
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
	if ref.Address.SubID > 0 {
		rights |= authorization.ACLRights(columns[ref.Address.SubID], authorization.PrincipalRef{}, principals, mask, grantOptions)
	}
	if !grantOptions && actorExists {
		rights |= predefinedPrivileges(d, principals)
	}
	return rights & mask, nil
}

// Own TEMP namespaces remain usable after revocation, but CREATE is contingent
// on the current effective role's database TEMP privilege, not authentication.
func (v accessView) ownTemporaryPrivileges(env accessEnvironment) (authorization.Privileges, error) {
	rights, err := v.Effective(env, v.Ref(catalog.CurrentDatabaseAddress), false)
	if err != nil {
		return 0, err
	}
	if rights&authorization.TemporaryPrivilege != 0 {
		return authorization.UsagePrivilege | authorization.CreatePrivilege, nil
	}
	return authorization.UsagePrivilege, nil
}

func (v accessView) Check(env accessEnvironment, req AccessRequirement) error {
	d, err := v.object(req.Object)
	if err != nil {
		return err
	}
	if t, ok := d.(*catalog.Type); ok && t.SQLType.IsArray() && !req.OwnerOnly && req.Privileges == authorization.UsagePrivilege {
		req.Object.Address.ObjectID = ids.OID(t.SQLType.Element().ID())
		return v.Check(env, req)
	}
	if err := v.roles.ValidatePrincipal(v.state.CurrentUser); err != nil {
		return err
	}
	if _, exists := v.roles.Find(v.state.CurrentUser); !exists && v.state.CurrentUser != v.missingCurrent {
		return fmt.Errorf("stuntgres: stale principal")
	}
	owner, _, _ := descriptorACL(d)
	if req.OwnerOnly {
		if req.Privileges != 0 {
			return fmt.Errorf("stuntgres: owner check has ordinary mask")
		}
		allowed := v.state.CurrentUser == owner
		if _, exists := v.roles.Find(v.state.CurrentUser); exists && !allowed {
			allowed, err = v.roles.InheritsRole(v.state.CurrentUser, owner)
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
			return sqlError("42501", "must be owner of type "+typeDiagnosticNameAt(v.catalog, t, path))
		}
		if d.Kind() == ids.NamespaceObject {
			kind = "schema"
		} else if d.Kind() == ids.CollationObject {
			kind = "collation"
		} else if d.Kind() == ids.ConversionObject {
			kind = "conversion"
		} else if d.Kind() == ids.OperatorObject {
			kind = "operator"
		} else if d.Kind() == ids.ProcedureObject {
			kind = "function"
		} else if d.Kind() == ids.DatabaseObject {
			kind = "database"
		} else if d.Kind() == ids.ForeignWrapperObject {
			kind = "foreign-data wrapper"
		} else if d.Kind() == ids.ForeignServerObject {
			kind = "foreign server"
		} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.SequenceRelation {
			kind = "sequence"
		} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.ForeignRelation {
			kind = "foreign table"
		}
		if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.ViewRelation {
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
	mask := classPrivileges(d)
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
		return sqlError("42501", "permission denied for type "+typeDiagnosticNameAt(v.catalog, t, path))
	}
	if d.Kind() == ids.NamespaceObject {
		kind = "schema"
	} else if d.Kind() == ids.DatabaseObject {
		kind = "database"
	} else if d.Kind() == ids.TablespaceObject {
		kind = "tablespace"
	} else if d.Kind() == ids.ProcedureObject {
		kind = "function"
	} else if d.Kind() == ids.ForeignWrapperObject {
		kind = "foreign-data wrapper"
	} else if d.Kind() == ids.ForeignServerObject {
		kind = "foreign server"
	} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.SequenceRelation {
		kind = "sequence"
	} else if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.ForeignRelation {
		kind = "foreign table"
	}
	if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.ViewRelation {
		kind = "view"
	}
	if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.MaterializedViewRelation {
		kind = "materialized view"
	}
	name := d.Name()
	if d.Kind() == ids.DatabaseObject {
		name = dependencyIdentifier(name)
	}
	return sqlError("42501", fmt.Sprintf("permission denied for %s %s", kind, name))
}
func (v accessView) CanUse(env accessEnvironment, ref ids.ObjectRef) error {
	return v.Check(env, AccessRequirement{Object: ref, Privileges: authorization.UsagePrivilege})
}
func (v accessView) CheckCreate(env accessEnvironment, ref ids.ObjectRef) error {
	return v.Check(env, AccessRequirement{Object: ref, Privileges: authorization.CreatePrivilege})
}

// CheckColumns checks mask on the columns of relation at the given dense
// column indices. With no indices, any live column, or the whole relation,
// can supply the privilege.
func (v accessView) CheckColumns(env accessEnvironment, ref ids.ObjectRef, relation *catalog.Relation, mask authorization.Privileges, indices []int) error {
	if relation.RelationKind == catalog.SequenceRelation && mask != authorization.SelectPrivilege {
		return v.Check(env, AccessRequirement{Object: ref, Privileges: mask})
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
		return v.Check(env, AccessRequirement{Object: ref, Privileges: mask})
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
		return v.Check(env, AccessRequirement{Object: column, Privileges: mask})
	}
	for _, index := range indices {
		if err := check(index); err != nil {
			return err
		}
	}
	return nil
}

func validateACL(r authorization.Read, d catalog.Descriptor) error {
	// Immutable builtin identities have no principal or role-owned ACL.
	switch t := d.(type) {
	case *catalog.ForeignObject:
		if t.Address().ClassID == ids.ForeignMappingClassID {
			if t.MappedRole == (authorization.PrincipalRef{}) {
				return nil
			}
			return r.ValidatePrincipal(t.MappedRole)
		}
	case *catalog.TSTemplate:
		return nil
	}
	owner, acl, cols := descriptorACL(d)
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
	if err := authorization.ValidateACL(r, acl, classPrivileges(d)); err != nil {
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
