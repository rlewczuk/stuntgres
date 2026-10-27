package authorization

import (
	"fmt"
	"iter"
	"math"
)

// PrincipalRef is an engine-owned identity, never a portable numeric OID.
type PrincipalRef struct {
	owner *Domain
	id    uint64
}

func (r PrincipalRef) ID() uint64 { return r.id }

type RoleDescriptor struct {
	Ref                                                                     PrincipalRef
	Name                                                                    string
	AuthIDVersion                                                           uint64
	Login, Superuser, CreateDB, CreateRole, Inherit, Replication, BypassRLS bool
	ConnectionLimit                                                         int32
	Password                                                                string // Empty is SQL NULL; verifiers are immutable strings.
	ValidUntil                                                              int64  // Typed timestamptz microseconds, including infinities.
	HasValidUntil                                                           bool
	Comment                                                                 string
	predefined                                                              bool
}
type Membership struct {
	OID                   uint32
	Role, Member, Grantor PrincipalRef
	Admin, Inherit, Set   bool
}
type Root struct {
	domain *Domain
	roles  map[PrincipalRef]RoleDescriptor
	names  map[string]PrincipalRef
	edges  []Membership
}
type Read struct {
	owner         *Domain
	state         *Root
	databaseOwner PrincipalRef
}

func (r Read) Lookup(name string) (RoleDescriptor, bool) {
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
func (r Read) ValidatePrincipal(ref PrincipalRef) error {
	if r.owner == nil || r.state == nil || ref.owner != r.owner || ref.id == 0 {
		return fmt.Errorf("stuntgres: foreign or zero principal")
	}
	return nil
}
func (r Read) Descriptor(ref PrincipalRef) (RoleDescriptor, error) {
	if err := r.ValidatePrincipal(ref); err != nil {
		return RoleDescriptor{}, err
	}
	d, ok := r.state.roles[ref]
	if !ok {
		return RoleDescriptor{}, fmt.Errorf("stuntgres: stale principal")
	}
	return d, nil
}
func (r Read) Memberships() []Membership {
	if r.state == nil {
		return nil
	}
	return append([]Membership(nil), r.state.edges...)
}

// Ordinary privileges and ownership follow INHERIT paths, never SET authority.
// Validate endpoints before shortcuts and bound reachability by principal identity.
func (r Read) InheritsRole(actor, target PrincipalRef) (bool, error) {
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
func (r Read) AdminOfRole(actor, target PrincipalRef) (bool, error) {
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
func cloneRoles(r *Root) Root {
	n := Root{domain: r.domain, roles: make(map[PrincipalRef]RoleDescriptor, len(r.roles)), names: make(map[string]PrincipalRef, len(r.names)), edges: r.edges}
	for k, v := range r.roles {
		n.roles[k] = v
	}
	for k, v := range r.names {
		n.names[k] = v
	}
	return n
}

type RoleState struct {
	Authenticated, SessionUser, CurrentUser, ResetRole PrincipalRef
	ActiveRole                                         bool
	RoleText, ResetRoleText                            string
}

// Domain is inert provenance, embedded in the engine that owns allocation.
// Its nonzero size guarantees distinct embedded instances have distinct addresses.
type Domain struct{ identity byte }

func (d *Domain) Principal(id uint64) PrincipalRef { return PrincipalRef{d, id} }
func (r *Root) Read(databaseOwner PrincipalRef) Read {
	if r == nil {
		return Read{}
	}
	return Read{r.domain, r, databaseOwner}
}
func (r Read) BelongsTo(d *Domain) bool    { return r.owner != nil && r.owner == d && r.state != nil }
func (r Read) DatabaseOwner() PrincipalRef { return r.databaseOwner }
func (r Read) Find(ref PrincipalRef) (RoleDescriptor, bool) {
	if r.state == nil {
		return RoleDescriptor{}, false
	}
	d, ok := r.state.roles[ref]
	return d, ok
}

func (r Read) groupReference(ref PrincipalRef) bool {
	return r.owner != nil && r.state != nil && ref.owner == r.owner && ref.id != 0 && ref.id <= math.MaxUint32
}

// GroupRole checks an already-enumerated descriptor without another map lookup.
func (r Read) GroupRole(ref PrincipalRef, role RoleDescriptor) bool {
	return !role.Login && r.groupReference(ref) && role.Ref == ref
}

// GroupMember accepts live representable endpoints regardless of LOGIN/options.
func (r Read) GroupMember(ref PrincipalRef) bool {
	if !r.groupReference(ref) {
		return false
	}
	role, ok := r.state.roles[ref]
	return ok && role.Ref == ref
}
func (r Read) RoleCount() int {
	if r.state == nil {
		return 0
	}
	return len(r.state.roles)
}
func (r Read) RoleEntries() iter.Seq2[PrincipalRef, RoleDescriptor] {
	return func(yield func(PrincipalRef, RoleDescriptor) bool) {
		if r.state != nil {
			for ref, d := range r.state.roles {
				if !yield(ref, d) {
					return
				}
			}
		}
	}
}
func (r Read) Edges() iter.Seq[Membership] {
	return func(yield func(Membership) bool) {
		if r.state != nil {
			for _, edge := range r.state.edges {
				if !yield(edge) {
					return
				}
			}
		}
	}
}
func (r Read) ValidReference(ref PrincipalRef) bool { return r.ValidatePrincipal(ref) == nil }
func (d RoleDescriptor) Predefined() bool           { return d.predefined }

// PutDescriptor is the immutable storage transform used by CREATE, ALTER and COMMENT.
// It changes the name index only when the descriptor's name actually changes.
func (r *Root) PutDescriptor(d RoleDescriptor) (Root, error) {
	if d.Ref.owner != r.domain || d.Ref.id == 0 {
		return Root{}, fmt.Errorf("stuntgres: foreign or zero principal")
	}
	if d.Name == "" {
		return Root{}, fmt.Errorf("stuntgres: empty role name")
	}
	if ref, ok := r.names[d.Name]; ok && ref != d.Ref {
		return Root{}, fmt.Errorf("stuntgres: duplicate role name")
	}
	n := cloneRoles(r)
	if old, ok := r.roles[d.Ref]; ok {
		d.predefined = old.predefined
		if old.Name != d.Name {
			delete(n.names, old.Name)
		}
	}
	n.roles[d.Ref] = d
	n.names[d.Name] = d.Ref
	return n, nil
}
func (r *Root) Retire(targets map[PrincipalRef]RoleDescriptor) Root {
	n := cloneRoles(r)
	n.edges = make([]Membership, 0, len(r.edges))
	for _, edge := range r.edges {
		if _, ok := targets[edge.Role]; ok {
			continue
		}
		if _, ok := targets[edge.Member]; ok {
			continue
		}
		n.edges = append(n.edges, edge)
	}
	for ref, d := range targets {
		delete(n.roles, ref)
		delete(n.names, d.Name)
	}
	return n
}

// Reprovenance creates Restore's private staging authority and fills its existing
// incarnation mapping; it is not used when retaining ordinary read snapshots.
func (r *Root) Reprovenance(domain *Domain, targets map[PrincipalRef]PrincipalRef) Root {
	n := Root{domain: domain, roles: make(map[PrincipalRef]RoleDescriptor, len(r.roles)), names: make(map[string]PrincipalRef, len(r.names)), edges: make([]Membership, len(r.edges))}
	for target, d := range r.roles {
		ref := domain.Principal(target.id)
		d.Ref = ref
		n.roles[ref] = d
		n.names[d.Name] = ref
		targets[ref] = target
	}
	for i, edge := range r.edges {
		edge.Role = domain.Principal(edge.Role.id)
		edge.Member = domain.Principal(edge.Member.id)
		edge.Grantor = domain.Principal(edge.Grantor.id)
		n.edges[i] = edge
	}
	return n
}

func (r Read) LookupRef(name string) PrincipalRef {
	if r.state == nil {
		return PrincipalRef{}
	}
	return r.state.names[name]
}

func (r *Root) RetainedMemberships(targets map[PrincipalRef]RoleDescriptor) iter.Seq[Membership] {
	return func(yield func(Membership) bool) {
		for _, edge := range r.edges {
			if _, ok := targets[edge.Role]; ok {
				continue
			}
			if _, ok := targets[edge.Member]; ok {
				continue
			}
			if !yield(edge) {
				return
			}
		}
	}
}
