package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (e *Engine) Roles() authorization.Read {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)
}
func (e *Engine) ResolveLogin(name string) (authorization.PrincipalRef, error) {
	d, ok := e.Roles().Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: authenticated role %q does not exist", name)
	}
	if !d.Login {
		return authorization.PrincipalRef{}, roleLoginError(name)
	}
	return d.Ref, nil
}

// SetRoleReferences publishes copied persistent dependencies owned by another
// internal feature. An empty payload unregisters that feature's references.
func (e *Engine) SetRoleReferences(ctx context.Context, feature string, refs []authorization.PrincipalRef) error {
	if feature == "" {
		return fmt.Errorf("stuntgres: empty role reference owner")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ref := range refs {
		if _, err := (e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)).Descriptor(ref); err != nil {
			return err
		}
	}
	return e.roles.setReferencesLocked(ctx, feature, refs)
}

type sessionAuthorization struct {
	session  *Session
	state    authorization.RoleState
	revision uint64
}

func (a *sessionAuthorization) EndSession(token ids.SessionToken, reason EndReason) error {
	if a.session == nil || a.session.token != token {
		return fmt.Errorf("stuntgres: foreign authorization attachment")
	}
	a.session.mu.Lock()
	defer a.session.mu.Unlock()
	if a.session.authorization == a {
		a.session.authorization = nil
	}
	return nil
}
func (op *Operation) RoleState() authorization.RoleState {
	if op == nil || op.self != op || op.session == nil {
		return authorization.RoleState{}
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorization == nil {
		return authorization.RoleState{}
	}
	return s.authorization.state
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
type AuthorizationView struct {
	engine        *Engine
	catalog       catalog.Read
	definition    definitionReader // Cooking-only sparse effects; never SQL execution authority.
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
	settings         *sessionSettings
	// Nested schema elements prepend their enclosing namespace without changing settings.
	pathPrefix ids.ObjectAddress
	// Trusted replay permissions may elevate without changing $user semantics.
	namespaceUser authorization.PrincipalRef
	// An owned TEMP table builder may see its namespace before session publication.
	stagedTemporary        ids.ObjectAddress
	session                *Session
	materializedPopulation bool
	// Only command admission can retain an actually installed, unnamed role ID.
	// Arbitrary stale/retired actor substitutions do not acquire this authority.
	missingCurrent authorization.PrincipalRef
}

// Statement-local Restore authority must cover both lock resolution and effects.
// INSERT/COMMENT stamp no owner; named directives and session identity stay intact.
func (e *Engine) statementAuthorizationLocked(op *Operation, stmt parser.Statement) (AuthorizationView, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return AuthorizationView{}, err
	}
	if _, view := stmt.(*parser.CreateView); view && op.restoreTableDestination {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore view authority")
		}
		auth.namespaceUser = auth.state.CurrentUser
		auth.state.CurrentUser = bootstrap.Ref
	}
	if creation, ok := stmt.(*parser.CreateQueryTable); ok && creation.Form == parser.CreateMaterializedView && op.restoreTableDestination {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore materialized view authority")
		}
		auth.namespaceUser = auth.state.CurrentUser
		auth.state.CurrentUser = bootstrap.Ref
	}
	if _, index := stmt.(*parser.CreateIndex); index && op.restoreTableDestination {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore index authority")
		}
		auth.namespaceUser = auth.state.CurrentUser
		auth.state.CurrentUser = bootstrap.Ref
	}
	if _, policy := stmt.(*parser.CreatePolicy); policy && op.restoreTableDestination {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore policy authority")
		}
		auth.namespaceUser = auth.state.CurrentUser
		auth.state.CurrentUser = bootstrap.Ref
	}
	if _, insert := stmt.(*parser.Insert); insert && op.restoreInsertAuthority {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore insertion authority")
		}
		auth.namespaceUser = auth.state.CurrentUser
		auth.state.CurrentUser = bootstrap.Ref
	}
	if op.restoreCommentAuthority {
		comment, ok := stmt.(*parser.Comment)
		if !ok || (comment.Kind != parser.CommentTable && comment.Kind != parser.CommentColumn && comment.Kind != parser.CommentSchema) {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore comment target")
		}
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore comment authority")
		}
		auth.namespaceUser = auth.state.CurrentUser
		auth.state.CurrentUser = bootstrap.Ref
	}
	return auth, nil
}

func (v AuthorizationView) Roles() authorization.Read { return v.roles }
func (v AuthorizationView) Stamp() AuthorizationStamp {
	if v.roleRecord == nil {
		return AuthorizationStamp{}
	}
	return AuthorizationStamp{v.catalog.Epoch(), v.roleRecord.revision, v.revision}
}
func (op *Operation) Authorization() (AuthorizationView, error) {
	if op == nil || op.self != op || op.session == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: foreign or missing operation")
	}
	e := op.session.Engine()
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.authorizationLocked(op)
}

// Lock order is Engine.mu then Session.mu. Views retain only immutable values.
func (e *Engine) authorizationLocked(op *Operation) (AuthorizationView, error) {
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.authorization
	if a == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: missing authorization")
	}
	view := e.catalog.read(e.catalog.state)
	if s.transactions != nil {
		view = e.privateCatalogLocked(s.transactions.catalog)
	}
	r := e.roles.read(e.catalog.owner(), view, e.operationRolesLocked(op))
	if _, err := r.Descriptor(a.state.Authenticated); err != nil {
		return AuthorizationView{}, err
	}
	// PREPARE can detach a newly created role while preserving SET ROLE's ID.
	// Admission validates that ID's authority, not its current name visibility.
	for _, ref := range [...]authorization.PrincipalRef{a.state.SessionUser, a.state.CurrentUser} {
		if err := r.ValidatePrincipal(ref); err != nil {
			return AuthorizationView{}, err
		}
	}
	identity := a.state
	if op.materializedOwner != (authorization.PrincipalRef{}) {
		if _, err := r.Descriptor(op.materializedOwner); err != nil {
			return AuthorizationView{}, err
		}
		identity.CurrentUser = op.materializedOwner
	}
	authorization := AuthorizationView{engine: e, pairedCatalog: view, roleRecord: e.operationRolesLocked(op), catalog: view, definition: op.definition, roles: r, databasePredecessor: e.catalog.state.Get(catalog.CurrentDatabaseAddress), state: identity, sessionRoleState: a.state, revision: a.revision, settings: s.settings, session: s, materializedPopulation: op.materializedPopulation}
	if _, exists := r.Find(identity.CurrentUser); !exists {
		authorization.missingCurrent = identity.CurrentUser
	}
	return authorization, nil
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
func (v AuthorizationView) object(ref ids.ObjectRef) (catalog.Descriptor, error) {
	if v.engine == nil || v.catalog.Owner() != v.engine.catalog.owner() || v.catalog.State() == nil || !v.roles.BelongsTo(&v.engine.roles.domain) || v.roles != v.engine.roles.read(v.engine.catalog.owner(), v.catalog, v.roleRecord) || v.pairedCatalog != v.catalog || v.roles.DatabaseOwner() != v.catalog.MustDatabase(catalog.CurrentDatabaseAddress).Owner || ref.Domain() != v.engine.catalog.Domain || ref.Epoch != v.catalog.Epoch() {
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
func (v AuthorizationView) Effective(ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error) {
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
	if ns, ok := d.(*catalog.Namespace); ok && !grantOptions && ownTemporaryNamespace(v, ns) {
		return v.ownTemporaryPrivileges()
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
func (v AuthorizationView) Check(req AccessRequirement) error {
	d, err := v.object(req.Object)
	if err != nil {
		return err
	}
	if t, ok := d.(*catalog.Type); ok && t.SQLType.IsArray() && !req.OwnerOnly && req.Privileges == authorization.UsagePrivilege {
		req.Object.Address.ObjectID = ids.OID(t.SQLType.Element().ID())
		return v.Check(req)
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
			path, _ := v.engine.referencePath(v)
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
	rights, err := v.Effective(req.Object, false)
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
		path, _ := v.engine.referencePath(v)
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
func (v AuthorizationView) CanUse(ref ids.ObjectRef) error {
	return v.Check(AccessRequirement{Object: ref, Privileges: authorization.UsagePrivilege})
}
func (v AuthorizationView) CheckCreate(ref ids.ObjectRef) error {
	return v.Check(AccessRequirement{Object: ref, Privileges: authorization.CreatePrivilege})
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
func descriptorReferences(d catalog.Descriptor) []authorization.PrincipalRef {
	switch t := d.(type) {
	case *catalog.ForeignObject:
		if t.Address().ClassID == ids.ForeignMappingClassID {
			if t.MappedRole == (authorization.PrincipalRef{}) {
				return nil
			}
			return []authorization.PrincipalRef{t.MappedRole}
		}
	case *catalog.TSTemplate:
		return nil
	}
	owner, acl, cols := descriptorACL(d)
	refs := []authorization.PrincipalRef{owner}
	if relation, ok := d.(*catalog.Relation); ok {
		for _, policy := range relation.Security.Policies {
			for _, role := range policy.Roles {
				if !role.Public {
					refs = append(refs, role.Ref)
				}
			}
		}
	}
	add := func(a authorization.ACLValue) {
		for _, x := range a.Entries {
			refs = append(refs, x.Grantor)
			if !x.Grantee.Public {
				refs = append(refs, x.Grantee.Role)
			}
		}
	}
	add(acl)
	for _, a := range cols {
		add(a)
	}
	return refs
}
func (v AuthorizationView) checkBinding(b binding, stmt parser.Statement) error {
	if b.viewWrite != nil {
		return v.checkViewWrite(b, stmt)
	}
	switch stmt.(type) {
	case *parser.Select, *parser.DeclareCursor, *parser.CreateQueryTable:
		return v.checkQuerySources(b.querySources)
	}
	if b.relation == nil {
		return nil
	}
	switch stmt.(type) {
	case *parser.Insert:
		if err := v.checkColumns(b, authorization.InsertPrivilege, b.targets); err != nil {
			return err
		}
		if b.conflict != nil && len(b.conflict.reads) != 0 {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.conflict.reads); err != nil {
				return err
			}
		}
		if b.conflict != nil && b.conflict.update != nil {
			if err := v.checkColumns(b, authorization.UpdatePrivilege, b.conflict.updateTargets); err != nil {
				return err
			}
			if len(b.conflict.actionReads) != 0 {
				if err := v.checkColumns(b, authorization.SelectPrivilege, b.conflict.actionReads); err != nil {
					return err
				}
			}
		}
		if b.returning != nil && (b.returning.readRequired || len(b.returning.reads) != 0) {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.returning.reads); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.querySources)
	case *parser.Merge:
		if b.merge.hasDelete {
			if err := v.Check(AccessRequirement{Object: b.ref, Privileges: authorization.DeletePrivilege}); err != nil {
				return err
			}
		}
		if b.merge.hasInsert {
			if err := v.checkColumns(b, authorization.InsertPrivilege, b.merge.insertTargets); err != nil {
				return err
			}
		}
		if len(b.merge.updateTargets) != 0 {
			if err := v.checkColumns(b, authorization.UpdatePrivilege, b.merge.updateTargets); err != nil {
				return err
			}
		}
		if b.merge.hasNothing || len(b.usedColumns) != 0 || b.returning != nil && b.returning.readRequired {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.usedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.querySources)
	case *parser.Delete:
		if err := v.Check(AccessRequirement{Object: b.ref, Privileges: authorization.DeletePrivilege}); err != nil {
			return err
		}
		if len(b.usedColumns) != 0 {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.usedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.querySources)
	case *parser.Update:
		if err := v.checkColumns(b, authorization.UpdatePrivilege, b.targets); err != nil {
			return err
		}
		if len(b.usedColumns) != 0 {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.usedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.querySources)
	default:
		return v.checkColumns(b, authorization.SelectPrivilege, b.usedColumns)
	}
}

func (v AuthorizationView) checkQuerySources(sources []queryLeaf) error {
	for _, source := range sources {
		if source.analysisOnly {
			continue
		}
		sourceAuth := v
		if source.principal != (authorization.PrincipalRef{}) {
			sourceAuth.state.CurrentUser = source.principal
			if err := sourceAuth.roles.ValidatePrincipal(source.principal); err != nil {
				return err
			}
		}
		if err := sourceAuth.checkColumns(binding{ref: source.ref, relation: source.relation}, authorization.SelectPrivilege, source.uses); err != nil {
			return err
		}
	}
	for _, source := range sources {
		sourceAuth := v
		if source.principal != (authorization.PrincipalRef{}) {
			sourceAuth.state.CurrentUser = source.principal
			if err := sourceAuth.roles.ValidatePrincipal(source.principal); err != nil {
				return err
			}
		}
		if effectiveQueryRowMark(source.relation, source.mark).mode != 0 {
			if err := sourceAuth.checkColumns(binding{ref: source.ref, relation: source.relation}, authorization.UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if source.relation.RelationKind == catalog.ViewRelation && (source.mark.mode != 0 || viewHasStoredRowMarks(source.relation, v.catalog)) {
			if err := sourceAuth.checkViewRowMarks(source.relation, source.mark, sourceAuth.state.CurrentUser); err != nil {
				return err
			}
		}
	}
	return nil
}
func (v AuthorizationView) checkColumns(b binding, mask authorization.Privileges, indices []int) error {
	if b.relation.RelationKind == catalog.SequenceRelation && mask != authorization.SelectPrivilege {
		return v.Check(AccessRequirement{Object: b.ref, Privileges: mask})
	}
	whole, err := v.Effective(b.ref, false)
	if err != nil {
		return err
	}
	if whole&mask == mask {
		return nil
	}
	if len(indices) == 0 {
		// An empty SELECT/INSERT manifest still needs authority, but any
		// live column can supply it. Attribute IDs survive dense-slot edits.
		for _, attribute := range b.relation.Attributes {
			ref := b.ref
			ref.Address.SubID = attribute
			rights, err := v.Effective(ref, false)
			if err != nil {
				return err
			}
			if rights&mask == mask {
				return nil
			}
		}
		return v.Check(AccessRequirement{Object: b.ref, Privileges: mask})
	}
	// The usual small relation needs no allocation to deduplicate actual uses.
	// Larger column-only ACL manifests use one bit per stable column position.
	var small uint64
	var seen []uint64
	if len(b.relation.Columns) > 64 {
		seen = make([]uint64, (len(b.relation.Columns)+63)/64)
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
		ref := b.ref
		ref.Address.SubID = b.relation.Attributes[index]
		return v.Check(AccessRequirement{Object: ref, Privileges: mask})
	}
	for _, index := range indices {
		if err := check(index); err != nil {
			return err
		}
	}
	return nil
}

// SetObjectACL publishes an owned immutable ACL payload for internal features.
func (e *Engine) SetObjectACL(ctx context.Context, ref ids.ObjectRef, acl authorization.ACLValue) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() {
		return fmt.Errorf("stuntgres: foreign or stale ACL object")
	}
	addr := ref.Address
	addr.SubID = 0
	d, ok := e.catalog.state.Find(addr)
	if !ok {
		return fmt.Errorf("stuntgres: missing ACL object")
	}
	if !acl.Explicit && len(acl.Entries) != 0 {
		return fmt.Errorf("stuntgres: entries in absent ACL")
	}
	acl.Entries = append([]authorization.ACLEntry(nil), acl.Entries...)
	var next catalog.Descriptor
	switch d := d.(type) {
	case *catalog.Database:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: database subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Namespace:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: namespace subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Type:
		if ref.Address.SubID != 0 || d.SQLType.IsArray() {
			return fmt.Errorf("stuntgres: invalid type ACL target")
		}
		n := *d
		n.ACL = acl
		n.ACLSyntheticDefault = false
		next = &n
	case *catalog.Procedure:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: procedure subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Parameter:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: parameter subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Tablespace:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: tablespace subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Relation:
		if ref.Address.SubID < 0 || ref.Address.SubID > 0 && !d.HasAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid ACL column")
		}
		n := *d
		if ref.Address.SubID == 0 {
			n.Security.ACL = acl
		} else {
			n.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(d.Security.ColumnACL)+1)
			for k, v := range d.Security.ColumnACL {
				n.Security.ColumnACL[k] = v
			}
			if acl.Explicit {
				n.Security.ColumnACL[ref.Address.SubID] = acl
			} else {
				delete(n.Security.ColumnACL, ref.Address.SubID)
			}
		}
		next = &n
	default:
		return fmt.Errorf("stuntgres: unsupported ACL object")
	}
	if err := validateACL(e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), next); err != nil {
		return err
	}
	if e.roles.published.revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	state := catalog.CloneState(e.catalog.state)
	if parameter, ok := next.(*catalog.Parameter); ok {
		installParameterGrant(state, parameter)
	} else {
		state.SetDescriptor(addr, next)
	}
	roles := e.roles.published.advance()
	if err := ctx.Err(); err != nil {
		return err
	}
	if n, ok := next.(*catalog.Relation); ok && n.RelationKind == catalog.SequenceRelation && ref.Address.SubID == 0 {
		stamp, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		n.SequencePlanStamp = stamp
	}
	if _, ok := next.(*catalog.Namespace); ok {
		if err := state.AdvanceNamespaceGeneration(); err != nil {
			return err
		}
	}
	e.catalog.state = state
	e.roles.published = roles
	return nil
}

// recaptureCatalog binds a substituted catalog to the same immutable role root.
func (v *AuthorizationView) recaptureCatalog(catalog catalog.Read) {
	v.catalog = catalog
	v.pairedCatalog = catalog
	v.roles = v.engine.roles.read(v.engine.catalog.owner(), catalog, v.roleRecord)
}

// roleCatalog is an immutable engine-owned publication stamp. All principal,
// descriptor, name and edge storage belongs to its embedded child root.
type roleCatalog struct {
	root     authorization.Root
	revision uint64
}

// advance returns a record with the same root and the next revision. Callers
// check revision exhaustion first.
func (r *roleCatalog) advance() *roleCatalog {
	return &roleCatalog{root: r.root, revision: r.revision + 1}
}
