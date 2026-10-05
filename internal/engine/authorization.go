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
	Ref              PrincipalRef
	Name             string
	Login, Superuser bool
	Comment          string
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
		return PrincipalRef{}, fmt.Errorf("stuntgres: role %q is not permitted to log in", name)
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
func (e *Engine) initializeRoles() {
	ref := PrincipalRef{e, 10}
	e.nextPrincipal = 11
	e.roles = &roleCatalog{revision: 1, roles: map[PrincipalRef]RoleDescriptor{ref: {Ref: ref, Name: "postgres", Login: true, Superuser: true}}, names: map[string]PrincipalRef{"postgres": ref}}
}

// AddMembership is the validated internal seam for membership command owners.
// This foundation does not traverse edges or expose SQL membership commands.
func (e *Engine) AddMembership(ctx context.Context, m Membership) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := RoleRead{e, e.roles}
	for _, ref := range [...]PrincipalRef{m.Role, m.Member, m.Grantor} {
		if _, err := r.Descriptor(ref); err != nil {
			return err
		}
	}
	if e.roles.revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	n := advanceRoleRevision(e.roles)
	n.edges = make([]Membership, len(e.roles.edges)+1)
	copy(n.edges, e.roles.edges)
	n.edges[len(e.roles.edges)] = m
	if err := ctx.Err(); err != nil {
		return err
	}
	e.roles = n
	return nil
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
	revision uint64
	settings *sessionSettings
	// Nested schema elements prepend their enclosing namespace without changing settings.
	pathPrefix ObjectAddress
}

// Statement-local Restore authority must cover both lock resolution and effects.
// INSERT/COMMENT stamp no owner; named directives and session identity stay intact.
func (e *Engine) statementAuthorizationLocked(op *Operation, stmt parser.Statement) (AuthorizationView, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return AuthorizationView{}, err
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
	return AuthorizationView{engine: e, catalog: CatalogRead{e, e.state}, roles: r, state: a.state, revision: a.revision, settings: s.settings}, nil
}
func descriptorACL(d Descriptor) (PrincipalRef, ACLValue, map[int32]ACLValue) {
	switch d := d.(type) {
	case *namespaceDescriptor:
		return d.owner, d.acl, nil
	case *relationDescriptor:
		return d.owner, d.acl, d.columnACL
	}
	return PrincipalRef{}, ACLValue{}, nil
}
func classPrivileges(d Descriptor) Privileges {
	if d.Kind() == NamespaceObject {
		return UsagePrivilege | CreatePrivilege
	}
	return SelectPrivilege | InsertPrivilege
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
func (v AuthorizationView) aclRights(acl ACLValue, owner PrincipalRef, mask Privileges, options bool) Privileges {
	if !acl.Explicit {
		if v.state.CurrentUser == owner {
			return mask
		}
		return 0
	}
	var rights Privileges
	for _, entry := range acl.Entries {
		if entry.Grantee.Public || entry.Grantee.Role == v.state.CurrentUser {
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
	if actor.Superuser || grantOptions && actor.Ref == owner {
		return mask, nil
	}
	rights := v.aclRights(acl, owner, mask, grantOptions)
	if ref.Address.SubID > 0 {
		rights |= v.aclRights(columns[ref.Address.SubID], PrincipalRef{}, mask, grantOptions)
	}
	return rights, nil
}
func (v AuthorizationView) Check(req AccessRequirement) error {
	d, err := v.object(req.Object)
	if err != nil {
		return err
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
		if actor.Superuser || actor.Ref == owner {
			return nil
		}
		kind := "table"
		if d.Kind() == NamespaceObject {
			kind = "schema"
		}
		if req.Object.Address.SubID > 0 && d.Kind() == RelationObject {
			kind = "relation"
		}
		return sqlError("42501", fmt.Sprintf("must be owner of %s %s", kind, d.Name()))
	}
	if req.Privileges == 0 || req.Privileges & ^classPrivileges(d) != 0 {
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
	if d.Kind() == NamespaceObject {
		kind = "schema"
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
	// Builtin type identities have no role-owned ACL in this foundation.
	if _, ok := d.(*typeDescriptor); ok {
		return nil
	}
	owner, acl, cols := descriptorACL(d)
	if _, err := r.Descriptor(owner); err != nil {
		return err
	}
	check := func(a ACLValue) error {
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
			if x.Privileges & ^classPrivileges(d) != 0 || x.GrantOptions & ^x.Privileges != 0 {
				return fmt.Errorf("stuntgres: invalid ACL mask")
			}
		}
		return nil
	}
	if err := check(acl); err != nil {
		return err
	}
	for sub, a := range cols {
		relation, ok := d.(*relationDescriptor)
		if !ok || sub <= 0 || !relation.hasAttribute(sub) {
			return fmt.Errorf("stuntgres: invalid ACL column")
		}
		if err := check(a); err != nil {
			return err
		}
	}
	return nil
}
func descriptorReferences(d Descriptor) []PrincipalRef {
	owner, acl, cols := descriptorACL(d)
	refs := []PrincipalRef{owner}
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
func (v AuthorizationView) checkBinding(b binding, insert bool) error {
	mask, indices := SelectPrivilege, b.usedColumns
	if insert {
		mask, indices = InsertPrivilege, b.targets
	}
	whole, err := v.Effective(b.ref, false)
	if err != nil {
		return err
	}
	if whole&mask == mask {
		return nil
	}
	if len(indices) == 0 {
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
	e.state = state
	e.roles = roles
	return nil
}
