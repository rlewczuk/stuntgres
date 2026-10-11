package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
)

// PrincipalRef is an engine-owned identity, never a portable numeric OID.
type PrincipalRef struct {
	owner *Engine
	id    uint64
}

func (r PrincipalRef) ID() uint64 { return r.id }

type RoleDescriptor struct {
	Ref                                                                     PrincipalRef
	Name                                                                    string
	Login, Superuser, CreateDB, CreateRole, Inherit, Replication, BypassRLS bool
	ConnectionLimit                                                         int32
	Password                                                                string // Empty is SQL NULL; verifiers are immutable strings.
	ValidUntil                                                              int64  // Typed timestamptz microseconds, including infinities.
	HasValidUntil                                                           bool
	Comment                                                                 string
	predefined                                                              bool
}
type Membership struct {
	Role, Member, Grantor PrincipalRef
	Admin, Inherit, Set   bool
}
type roleCatalog struct {
	revision uint64
	roles    map[PrincipalRef]RoleDescriptor
	names    map[string]PrincipalRef
	edges    []Membership
}
type RoleRead struct {
	owner *Engine
	state *roleCatalog
}

func (r RoleRead) Lookup(name string) (RoleDescriptor, bool) {
	if r.state == nil {
		return RoleDescriptor{}, false
	}
	ref, ok := r.state.names[name]
	if !ok {
		return RoleDescriptor{}, false
	}
	d, ok := r.state.roles[ref]
	return d, ok
}
func (r RoleRead) Descriptor(ref PrincipalRef) (RoleDescriptor, error) {
	if r.owner == nil || r.state == nil || ref.owner != r.owner || ref.id == 0 {
		return RoleDescriptor{}, fmt.Errorf("stuntgres: foreign or zero principal")
	}
	d, ok := r.state.roles[ref]
	if !ok {
		return RoleDescriptor{}, fmt.Errorf("stuntgres: stale principal")
	}
	return d, nil
}
func (r RoleRead) Memberships() []Membership {
	if r.state == nil {
		return nil
	}
	return append([]Membership(nil), r.state.edges...)
}

// Ordinary privileges and ownership follow INHERIT paths, never SET authority.
// Validate endpoints before shortcuts and bound reachability by principal identity.
func (r RoleRead) inheritsRole(actor, target PrincipalRef) (bool, error) {
	role, err := r.Descriptor(actor)
	if err != nil {
		return false, err
	}
	if _, err := r.Descriptor(target); err != nil {
		return false, err
	}
	if actor == target || role.Superuser {
		return true, nil
	}
	if len(r.state.edges) == 0 && r.implicitDatabaseOwner(actor) == (PrincipalRef{}) {
		return false, nil
	}
	seen := map[PrincipalRef]bool{actor: true}
	queue := []PrincipalRef{actor}
	for len(queue) != 0 {
		member := queue[0]
		queue = queue[1:]
		for _, edge := range r.state.edges {
			if !edge.Inherit || edge.Member != member || seen[edge.Role] {
				continue
			}
			if _, err := r.Descriptor(edge.Role); err != nil {
				return false, err
			}
			if edge.Role == target {
				return true, nil
			}
			seen[edge.Role] = true
			queue = append(queue, edge.Role)
		}
		if next := r.implicitDatabaseOwner(member); next != (PrincipalRef{}) && !seen[next] {
			if _, err := r.Descriptor(next); err != nil {
				return false, err
			}
			if next == target {
				return true, nil
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false, nil
}

// ADMIN traverses all memberships, not just INHERIT or SET edges. Only the
// edge into the target must carry ADMIN; membership in the target is not ADMIN.
func (r RoleRead) adminOfRole(actor, target PrincipalRef) (bool, error) {
	d, err := r.Descriptor(actor)
	if err != nil {
		return false, err
	}
	if _, err = r.Descriptor(target); err != nil {
		return false, err
	}
	if d.Superuser {
		return true, nil
	}
	if actor == target {
		return false, nil
	}
	seen := map[PrincipalRef]bool{actor: true}
	queue := []PrincipalRef{actor}
	for len(queue) > 0 {
		member := queue[0]
		queue = queue[1:]
		for _, edge := range r.state.edges {
			if edge.Member != member {
				continue
			}
			for _, ref := range [...]PrincipalRef{edge.Role, edge.Member, edge.Grantor} {
				if _, err = r.Descriptor(ref); err != nil {
					return false, err
				}
			}
			if edge.Role == target && edge.Admin {
				return true, nil
			}
			if !seen[edge.Role] {
				seen[edge.Role] = true
				queue = append(queue, edge.Role)
			}
		}
		if next := r.implicitDatabaseOwner(member); next != (PrincipalRef{}) && !seen[next] {
			if _, err := r.Descriptor(next); err != nil {
				return false, err
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false, nil
}
func (e *Engine) Roles() RoleRead {
	e.mu.Lock()
	defer e.mu.Unlock()
	return RoleRead{e, e.roles}
}
func (e *Engine) ResolveLogin(name string) (PrincipalRef, error) {
	d, ok := e.Roles().Lookup(name)
	if !ok {
		return PrincipalRef{}, fmt.Errorf("stuntgres: authenticated role %q does not exist", name)
	}
	if !d.Login {
		return PrincipalRef{}, roleLoginError(name)
	}
	return d.Ref, nil
}
func cloneRoles(r *roleCatalog) *roleCatalog {
	n := &roleCatalog{revision: r.revision + 1, roles: make(map[PrincipalRef]RoleDescriptor, len(r.roles)), names: make(map[string]PrincipalRef, len(r.names)), edges: r.edges}
	for k, v := range r.roles {
		n.roles[k] = v
	}
	for k, v := range r.names {
		n.names[k] = v
	}
	return n
}
func advanceRoleRevision(r *roleCatalog) *roleCatalog {
	n := *r
	n.revision++
	return &n
}

// SetRoleReferences publishes copied persistent dependencies owned by another
// internal feature. An empty payload unregisters that feature's references.
func (e *Engine) SetRoleReferences(ctx context.Context, feature string, refs []PrincipalRef) error {
	if feature == "" {
		return fmt.Errorf("stuntgres: empty role reference owner")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ref := range refs {
		if _, err := (RoleRead{e, e.roles}).Descriptor(ref); err != nil {
			return err
		}
	}
	if e.roles.revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	next := make(map[string][]PrincipalRef, len(e.roleReferences)+1)
	for key, payload := range e.roleReferences {
		next[key] = payload
	}
	if len(refs) == 0 {
		delete(next, feature)
	} else {
		next[feature] = append([]PrincipalRef(nil), refs...)
	}
	roles := advanceRoleRevision(e.roles)
	if err := ctx.Err(); err != nil {
		return err
	}
	e.roleReferences = next
	e.roles = roles
	return nil
}

type RoleState struct{ Authenticated, SessionUser, CurrentUser, ResetRole PrincipalRef }
type sessionAuthorization struct {
	session  *Session
	state    RoleState
	revision uint64
}

func (a *sessionAuthorization) EndSession(token SessionToken, reason EndReason) error {
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
func (op *Operation) RoleState() RoleState {
	if op == nil || op.self != op || op.session == nil {
		return RoleState{}
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorization == nil {
		return RoleState{}
	}
	return s.authorization.state
}

type Privileges uint32

const (
	SelectPrivilege Privileges = 1 << iota
	InsertPrivilege
	UsagePrivilege
	CreatePrivilege
	DeletePrivilege
	UpdatePrivilege
	MaintainPrivilege
	TruncatePrivilege
	ReferencesPrivilege
	TriggerPrivilege
)

type ACLGrantee struct {
	Public bool
	Role   PrincipalRef
}
type ACLEntry struct {
	Grantor                  PrincipalRef
	Grantee                  ACLGrantee
	Privileges, GrantOptions Privileges
}
type ACLValue struct {
	Explicit bool
	Entries  []ACLEntry
}

// schemaACLOwner follows aclnewowner: transfer both ownership references and
// combine grants that acquire the same grantor/grantee, without mutating the
// retained descriptor. NULL and explicitly empty ACLs stay distinct.
func schemaACLOwner(acl ACLValue, old, next PrincipalRef) ACLValue {
	if !acl.Explicit || len(acl.Entries) == 0 {
		return acl
	}
	out := ACLValue{Explicit: true, Entries: append([]ACLEntry(nil), acl.Entries...)}
	newPresent := false
	for i := range out.Entries {
		entry := &out.Entries[i]
		if entry.Grantor == old {
			entry.Grantor = next
		} else if entry.Grantor == next {
			newPresent = true
		}
		if !entry.Grantee.Public {
			if entry.Grantee.Role == old {
				entry.Grantee.Role = next
			} else if entry.Grantee.Role == next {
				newPresent = true
			}
		}
	}
	if newPresent {
		dst := 0
		for i := range out.Entries {
			entry := out.Entries[i]
			if entry.Privileges|entry.GrantOptions == 0 {
				continue
			}
			for j := i + 1; j < len(out.Entries); j++ {
				other := &out.Entries[j]
				if other.Grantor == entry.Grantor && other.Grantee == entry.Grantee {
					entry.Privileges |= other.Privileges
					entry.GrantOptions |= other.GrantOptions
					other.Privileges, other.GrantOptions = 0, 0
				}
			}
			out.Entries[dst] = entry
			dst++
		}
		out.Entries = out.Entries[:dst]
	}
	return out
}

type AccessRequirement struct {
	Object     ObjectRef
	Privileges Privileges
	OwnerOnly  bool
	OwnerNoun  string // Utility diagnostic context; never changes authority.
}
type AuthorizationStamp struct {
	Catalog        CatalogEpoch
	Roles, Session uint64
}
type AuthorizationView struct {
	engine   *Engine
	catalog  CatalogRead
	roles    RoleRead
	state    RoleState
	// Actual session identities remain distinct from trusted import authority.
	sessionRoleState RoleState
	revision uint64
	settings *sessionSettings
	// Nested schema elements prepend their enclosing namespace without changing settings.
	pathPrefix ObjectAddress
	// An owned TEMP table builder may see its namespace before session publication.
	stagedTemporary        ObjectAddress
	session                *Session
	materializedPopulation bool
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
		auth.state.CurrentUser = bootstrap.Ref
	}
	if creation, ok := stmt.(*parser.CreateQueryTable); ok && creation.Form == parser.CreateMaterializedView && op.restoreTableDestination {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore materialized view authority")
		}
		auth.state.CurrentUser = bootstrap.Ref
	}
	if _, index := stmt.(*parser.CreateIndex); index && op.restoreTableDestination {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore index authority")
		}
		auth.state.CurrentUser = bootstrap.Ref
	}
	if _, policy := stmt.(*parser.CreatePolicy); policy && op.restoreTableDestination {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore policy authority")
		}
		auth.state.CurrentUser = bootstrap.Ref
	}
	if _, insert := stmt.(*parser.Insert); insert && op.restoreInsertAuthority {
		bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore insertion authority")
		}
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
		auth.state.CurrentUser = bootstrap.Ref
	}
	return auth, nil
}

func (v AuthorizationView) Roles() RoleRead { return v.roles }
func (v AuthorizationView) Stamp() AuthorizationStamp {
	if v.roles.state == nil {
		return AuthorizationStamp{}
	}
	return AuthorizationStamp{v.catalog.Epoch(), v.roles.state.revision, v.revision}
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
	r := RoleRead{e, e.roles}
	for _, ref := range [...]PrincipalRef{a.state.Authenticated, a.state.SessionUser, a.state.CurrentUser} {
		if _, err := r.Descriptor(ref); err != nil {
			return AuthorizationView{}, err
		}
	}
	view := CatalogRead{e, e.state}
	if s.transactions != nil {
		view = e.privateCatalogLocked(s.transactions.catalog)
	}
	identity := a.state
	if op.materializedOwner != (PrincipalRef{}) {
		if _, err := r.Descriptor(op.materializedOwner); err != nil {
			return AuthorizationView{}, err
		}
		identity.CurrentUser = op.materializedOwner
	}
	return AuthorizationView{engine: e, catalog: view, roles: r, state: identity, sessionRoleState: a.state, revision: a.revision, settings: s.settings, session: s, materializedPopulation: op.materializedPopulation}, nil
}
func descriptorACL(d Descriptor) (PrincipalRef, ACLValue, map[int32]ACLValue) {
	switch d := d.(type) {
	case *namespaceDescriptor:
		return d.owner, d.acl, nil
	case *relationDescriptor:
		return d.owner, d.acl, d.columnACL
	case *typeDescriptor:
		return d.owner, d.acl, nil
	case *collationDescriptor:
		return d.owner, ACLValue{}, nil
	}
	return PrincipalRef{}, ACLValue{}, nil
}
func classPrivileges(d Descriptor) Privileges {
	if d.Kind() == TypeObject {
		return UsagePrivilege
	}
	if d.Kind() == NamespaceObject {
		return UsagePrivilege | CreatePrivilege
	}
	if r, ok := d.(*relationDescriptor); ok && r.relationKind == sequenceRelation {
		return SelectPrivilege | UsagePrivilege | UpdatePrivilege
	}
	return tableGrantPrivileges
}
func (v AuthorizationView) object(ref ObjectRef) (Descriptor, error) {
	if v.engine == nil || v.catalog.state == nil || ref.owner != v.engine.owner || ref.Epoch != v.catalog.Epoch() {
		return nil, fmt.Errorf("stuntgres: foreign or stale authorization object")
	}
	d, ok := v.catalog.Descriptor(ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID})
	if !ok || ref.Address.SubID < 0 {
		return nil, fmt.Errorf("stuntgres: invalid authorization object")
	}
	if ref.Address.SubID > 0 {
		r, ok := d.(*relationDescriptor)
		if !ok || !r.hasAttribute(ref.Address.SubID) {
			return nil, fmt.Errorf("stuntgres: invalid authorization column")
		}
	}
	owner, _, _ := descriptorACL(d)
	if _, err := v.roles.Descriptor(owner); err != nil {
		return nil, err
	}
	return d, nil
}
func aclRights(acl ACLValue, owner PrincipalRef, principals []PrincipalRef, mask Privileges, options bool) Privileges {
	if !acl.Explicit {
		for _, ref := range principals {
			if ref == owner {
				return mask
			}
		}
		return 0
	}
	var rights Privileges
	for _, entry := range acl.Entries {
		allowed := entry.Grantee.Public
		if !allowed {
			for _, ref := range principals {
				if ref == entry.Grantee.Role {
					allowed = true
					break
				}
			}
		}
		if allowed {
			if options {
				rights |= entry.GrantOptions
			} else {
				rights |= entry.Privileges
			}
		}
	}
	return rights
}
func (v AuthorizationView) Effective(ref ObjectRef, grantOptions bool) (Privileges, error) {
	d, err := v.object(ref)
	if err != nil {
		return 0, err
	}
	owner, acl, columns := descriptorACL(d)
	actor, err := v.roles.Descriptor(v.state.CurrentUser)
	if err != nil {
		return 0, err
	}
	mask := classPrivileges(d)
	if ref.Address.SubID > 0 {
		mask = columnGrantPrivileges
	}
	if actor.Superuser {
		return mask, nil
	}
	principals, err := v.roles.roleClosure(context.Background(), actor.Ref, true)
	if err != nil {
		return 0, err
	}
	if grantOptions {
		for _, ref := range principals {
			if ref == owner {
				return mask, nil
			}
		}
	}
	rights := aclRights(acl, owner, principals, mask, grantOptions)
	if ref.Address.SubID > 0 {
		rights |= aclRights(columns[ref.Address.SubID], PrincipalRef{}, principals, mask, grantOptions)
	}
	if !grantOptions {
		rights |= predefinedPrivileges(d, principals)
	}
	return rights & mask, nil
}
func (v AuthorizationView) Check(req AccessRequirement) error {
	d, err := v.object(req.Object)
	if err != nil {
		return err
	}
	if t, ok := d.(*typeDescriptor); ok && t.typ.IsArray() && !req.OwnerOnly && req.Privileges == UsagePrivilege {
		req.Object.Address.ObjectID = OID(t.typ.Element().ID())
		return v.Check(req)
	}
	actor, err := v.roles.Descriptor(v.state.CurrentUser)
	if err != nil {
		return err
	}
	owner, _, _ := descriptorACL(d)
	if req.OwnerOnly {
		if req.Privileges != 0 {
			return fmt.Errorf("stuntgres: owner check has ordinary mask")
		}
		allowed, err := v.roles.inheritsRole(actor.Ref, owner)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
		kind := "table"
		if t, ok := d.(*typeDescriptor); ok {
			path, _ := v.engine.referencePath(v)
			return sqlError("42501", "must be owner of type "+typeDiagnosticNameAt(v.catalog, t, path))
		}
		if d.Kind() == NamespaceObject {
			kind = "schema"
		} else if d.Kind() == CollationObject {
			kind = "collation"
		} else if r, ok := d.(*relationDescriptor); ok && r.relationKind == sequenceRelation {
			kind = "sequence"
		}
		if r, ok := d.(*relationDescriptor); ok && r.relationKind == viewRelation {
			kind = "view"
		}
		if r, ok := d.(*relationDescriptor); ok && r.relationKind == materializedViewRelation {
			kind = "materialized view"
		}
		if r, ok := d.(*relationDescriptor); ok && r.relationKind == indexRelation {
			kind = "index"
		}
		if req.Object.Address.SubID > 0 && d.Kind() == RelationObject {
			kind = "relation"
		}
		if req.OwnerNoun != "" {
			kind = req.OwnerNoun
		}
		return sqlError("42501", fmt.Sprintf("must be owner of %s %s", kind, d.Name()))
	}
	mask := classPrivileges(d)
	if req.Object.Address.SubID > 0 {
		mask = columnGrantPrivileges
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
	if t, ok := d.(*typeDescriptor); ok {
		path, _ := v.engine.referencePath(v)
		return sqlError("42501", "permission denied for type "+typeDiagnosticNameAt(v.catalog, t, path))
	}
	if d.Kind() == NamespaceObject {
		kind = "schema"
	} else if r, ok := d.(*relationDescriptor); ok && r.relationKind == sequenceRelation {
		kind = "sequence"
	}
	if r, ok := d.(*relationDescriptor); ok && r.relationKind == viewRelation {
		kind = "view"
	}
	if r, ok := d.(*relationDescriptor); ok && r.relationKind == materializedViewRelation {
		kind = "materialized view"
	}
	return sqlError("42501", fmt.Sprintf("permission denied for %s %s", kind, d.Name()))
}
func (v AuthorizationView) CanUse(ref ObjectRef) error {
	return v.Check(AccessRequirement{Object: ref, Privileges: UsagePrivilege})
}
func (v AuthorizationView) CheckCreate(ref ObjectRef) error {
	return v.Check(AccessRequirement{Object: ref, Privileges: CreatePrivilege})
}
func validateACL(r RoleRead, d Descriptor) error {
	// Immutable builtin identities have no principal or role-owned ACL.
	switch t := d.(type) {
	case *typeDescriptor:
		if t.owningRelation == (ObjectAddress{}) && !t.typ.IsUserDefined() && !(t.typ.IsArray() && t.typ.Element().IsUserDefined()) {
			return nil
		}
	case *conversionDescriptor:
		return nil
	}
	owner, acl, cols := descriptorACL(d)
	if _, err := r.Descriptor(owner); err != nil {
		return err
	}
	if relation, ok := d.(*relationDescriptor); ok {
		for _, policy := range relation.policies {
			for _, role := range policy.roles {
				if role.public {
					if role.ref != (PrincipalRef{}) {
						return fmt.Errorf("stuntgres: PUBLIC policy principal")
					}
				} else if _, err := r.Descriptor(role.ref); err != nil {
					return err
				}
			}
		}
	}
	check := func(a ACLValue, mask Privileges) error {
		if !a.Explicit && len(a.Entries) != 0 {
			return fmt.Errorf("stuntgres: entries in absent ACL")
		}
		for _, x := range a.Entries {
			if _, err := r.Descriptor(x.Grantor); err != nil {
				return err
			}
			if x.Grantee.Public {
				if x.Grantee.Role != (PrincipalRef{}) {
					return fmt.Errorf("stuntgres: PUBLIC principal")
				}
				if x.GrantOptions != 0 {
					return fmt.Errorf("stuntgres: PUBLIC grant options")
				}
			} else if _, err := r.Descriptor(x.Grantee.Role); err != nil {
				return err
			}
			if x.Privileges & ^mask != 0 || x.GrantOptions & ^x.Privileges != 0 {
				return fmt.Errorf("stuntgres: invalid ACL mask")
			}
		}
		return nil
	}
	if err := check(acl, classPrivileges(d)); err != nil {
		return err
	}
	for sub, a := range cols {
		relation, ok := d.(*relationDescriptor)
		if !ok || sub <= 0 || !relation.hasAttribute(sub) {
			return fmt.Errorf("stuntgres: invalid ACL column")
		}
		if err := check(a, columnGrantPrivileges); err != nil {
			return err
		}
	}
	return nil
}
func descriptorReferences(d Descriptor) []PrincipalRef {
	switch t := d.(type) {
	case *typeDescriptor:
		if t.owningRelation == (ObjectAddress{}) && !t.typ.IsUserDefined() && !(t.typ.IsArray() && t.typ.Element().IsUserDefined()) {
			return nil
		}
	case *conversionDescriptor:
		return nil
	}
	owner, acl, cols := descriptorACL(d)
	refs := []PrincipalRef{owner}
	if relation, ok := d.(*relationDescriptor); ok {
		for _, policy := range relation.policies {
			for _, role := range policy.roles {
				if !role.public {
					refs = append(refs, role.ref)
				}
			}
		}
	}
	add := func(a ACLValue) {
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
		if err := v.checkColumns(b, InsertPrivilege, b.targets); err != nil {
			return err
		}
		if b.conflict != nil && len(b.conflict.reads) != 0 {
			if err := v.checkColumns(b, SelectPrivilege, b.conflict.reads); err != nil {
				return err
			}
		}
		if b.conflict != nil && b.conflict.update != nil {
			if err := v.checkColumns(b, UpdatePrivilege, b.conflict.updateTargets); err != nil {
				return err
			}
			if len(b.conflict.actionReads) != 0 {
				if err := v.checkColumns(b, SelectPrivilege, b.conflict.actionReads); err != nil {
					return err
				}
			}
		}
		return v.checkQuerySources(b.querySources)
	case *parser.Merge:
		if b.merge.hasInsert {
			if err := v.checkColumns(b, InsertPrivilege, b.merge.insertTargets); err != nil {
				return err
			}
		}
		if len(b.merge.updateTargets) != 0 {
			if err := v.checkColumns(b, UpdatePrivilege, b.merge.updateTargets); err != nil {
				return err
			}
		}
		if len(b.usedColumns) != 0 {
			if err := v.checkColumns(b, SelectPrivilege, b.usedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.querySources)
	case *parser.Delete:
		if err := v.Check(AccessRequirement{Object: b.ref, Privileges: DeletePrivilege}); err != nil {
			return err
		}
		if len(b.usedColumns) != 0 {
			if err := v.checkColumns(b, SelectPrivilege, b.usedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.querySources)
	case *parser.Update:
		if err := v.checkColumns(b, UpdatePrivilege, b.targets); err != nil {
			return err
		}
		if len(b.usedColumns) != 0 {
			if err := v.checkColumns(b, SelectPrivilege, b.usedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.querySources)
	default:
		return v.checkColumns(b, SelectPrivilege, b.usedColumns)
	}
}

func (v AuthorizationView) checkQuerySources(sources []queryLeaf) error {
	for _, source := range sources {
		if source.analysisOnly {
			continue
		}
		if err := v.checkColumns(binding{ref: source.ref, relation: source.relation}, SelectPrivilege, source.uses); err != nil {
			return err
		}
	}
	for _, source := range sources {
		if source.mark.mode != 0 {
			if err := v.checkColumns(binding{ref: source.ref, relation: source.relation}, UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if source.relation.relationKind == viewRelation && (source.mark.mode != 0 || viewHasStoredRowMarks(source.relation, v.catalog)) {
			if err := v.checkViewRowMarks(source.relation, source.mark, v.state.CurrentUser); err != nil {
				return err
			}
		}
	}
	return nil
}
func (v AuthorizationView) checkColumns(b binding, mask Privileges, indices []int) error {
	if b.relation.relationKind == sequenceRelation {
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
		for _, attribute := range b.relation.attributes {
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
	if len(b.relation.columns) > 64 {
		seen = make([]uint64, (len(b.relation.columns)+63)/64)
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
		ref.Address.SubID = b.relation.attributes[index]
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
func (e *Engine) SetObjectACL(ctx context.Context, ref ObjectRef, acl ACLValue) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.owner != e.owner || ref.Epoch != e.state.epoch {
		return fmt.Errorf("stuntgres: foreign or stale ACL object")
	}
	addr := ref.Address
	addr.SubID = 0
	d, ok := e.state.descriptors[addr]
	if !ok {
		return fmt.Errorf("stuntgres: missing ACL object")
	}
	if !acl.Explicit && len(acl.Entries) != 0 {
		return fmt.Errorf("stuntgres: entries in absent ACL")
	}
	acl.Entries = append([]ACLEntry(nil), acl.Entries...)
	var next Descriptor
	switch d := d.(type) {
	case *namespaceDescriptor:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: namespace subobject")
		}
		n := *d
		n.acl = acl
		next = &n
	case *relationDescriptor:
		if ref.Address.SubID < 0 || ref.Address.SubID > 0 && !d.hasAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid ACL column")
		}
		n := *d
		if ref.Address.SubID == 0 {
			n.acl = acl
		} else {
			n.columnACL = make(map[int32]ACLValue, len(d.columnACL)+1)
			for k, v := range d.columnACL {
				n.columnACL[k] = v
			}
			if acl.Explicit {
				n.columnACL[ref.Address.SubID] = acl
			} else {
				delete(n.columnACL, ref.Address.SubID)
			}
		}
		next = &n
	default:
		return fmt.Errorf("stuntgres: unsupported ACL object")
	}
	if err := validateACL(RoleRead{e, e.roles}, next); err != nil {
		return err
	}
	if e.roles.revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	state := cloneCatalog(e.state)
	state.descriptors[addr] = next
	roles := advanceRoleRevision(e.roles)
	if err := ctx.Err(); err != nil {
		return err
	}
	if n, ok := next.(*relationDescriptor); ok && n.relationKind == sequenceRelation && ref.Address.SubID == 0 {
		stamp, err := e.revisionLocked()
		if err != nil {
			return err
		}
		n.sequencePlanStamp = stamp
	}
	if _, ok := next.(*namespaceDescriptor); ok {
		if err := state.advanceNamespaceGeneration(); err != nil {
			return err
		}
	}
	e.state = state
	e.roles = roles
	return nil
}
