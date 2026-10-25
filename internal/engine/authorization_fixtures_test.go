package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func cloneRoleFixture(r *access.RoleRecord) *access.RoleRecord {
	return &access.RoleRecord{Root: r.Root, Revision: r.Revision + 1}
}
func emptyRoleFixture(e *Engine) *access.RoleRecord {
	root := authorization.Seed(&e.RoleStore.Domain)
	targets := make(map[authorization.PrincipalRef]authorization.RoleDescriptor)
	for ref, d := range root.Read(authorization.PrincipalRef{}).RoleEntries() {
		targets[ref] = d
	}
	return &access.RoleRecord{Root: root.Retire(targets), Revision: 1}
}
func fixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}
func putFixtureRole(r *access.RoleRecord, d authorization.RoleDescriptor) {
	root, err := r.Root.PutDescriptor(d)
	if err != nil {
		panic(err)
	}
	r.Root = root
}
func retireFixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) {
	d := fixtureRole(r, ref)
	r.Root = r.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{ref: d})
}
func fixtureEdges(r *access.RoleRecord) []authorization.Membership {
	return r.Root.Read(authorization.PrincipalRef{}).Memberships()
}

// Build private graph fixtures through genuine storage and membership candidates.
// Temporary descriptor names/flags exist only on unpublished roots and are restored.
func setFixtureEdges(r *access.RoleRecord, edges []authorization.Membership) {
	candidate := *r
	destination := r
	r = &candidate
	read := r.Root.Read(authorization.PrincipalRef{})
	origins := make(map[authorization.PrincipalRef]bool)
	for edge := range read.Edges() {
		origins[edge.Grantor] = true
	}
	if len(origins) != 0 {
		root, err := authorization.OwnedMembershipCandidate(context.Background(), read, origins)
		if err != nil {
			panic(err)
		}
		r.Root = root
	}
	for _, edge := range edges {
		original := fixtureRole(r, edge.Grantor)
		oldBootstrap, hasBootstrap := r.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			temp := oldBootstrap
			temp.Name = fmt.Sprintf("fixture_bootstrap_%d", temp.Ref.ID())
			putFixtureRole(r, temp)
		}
		elevated := original
		elevated.Name = "postgres"
		elevated.Superuser = true
		putFixtureRole(r, elevated)
		target := fixtureRole(r, edge.Role)
		member := fixtureRole(r, edge.Member)
		request := &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: target.Name}}, Members: []parser.RoleSpec{{Name: member.Name}}, Options: []parser.MembershipOption{{Name: "admin", Value: edge.Admin}, {Name: "inherit", Value: edge.Inherit}, {Name: "set", Value: edge.Set}}}
		root, changed, _, err := authorization.MembershipCandidate(context.Background(), r.Root.Read(authorization.PrincipalRef{}), authorization.RoleState{CurrentUser: edge.Grantor, SessionUser: edge.Grantor}, request, nil, false, false)
		if err != nil {
			panic(err)
		}
		if changed {
			r.Root = root
		}
		putFixtureRole(r, original)
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			putFixtureRole(r, oldBootstrap)
		}
	}
	destination.Root = candidate.Root
}

func objectACLUnitEntry(source, target authorization.PrincipalRef, use, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: source, Grantee: authorization.ACLGrantee{Role: target}, Privileges: use, GrantOptions: options}
}

// descriptorReferences lists every principal a descriptor references: owner,
// policy roles, ACL grantors and grantees, including column ACLs.
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
	owner, acl, cols := access.DescriptorACL(d)
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

// setObjectACL publishes acl on ref directly, outside GRANT and REVOKE,
// with the validation, revision and publication steps of a grant.
func setObjectACL(e *Engine, ctx context.Context, ref ids.ObjectRef, acl authorization.ACLValue) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() {
		return fmt.Errorf("stuntgres: foreign or stale ACL object")
	}
	addr := ref.Address
	addr.SubID = 0
	d, ok := e.Catalog.State.Find(addr)
	if !ok {
		return fmt.Errorf("stuntgres: missing ACL object")
	}
	if !acl.Explicit && len(acl.Entries) != 0 {
		return fmt.Errorf("stuntgres: entries in absent ACL")
	}
	acl.Entries = append([]authorization.ACLEntry(nil), acl.Entries...)
	next, err := withObjectACL(d, ref.Address.SubID, acl)
	if err != nil {
		return err
	}
	if err := access.ValidateACL(e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), next); err != nil {
		return err
	}
	if e.RoleStore.Published.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	state := catalog.CloneState(e.Catalog.State)
	if parameter, ok := next.(*catalog.Parameter); ok {
		catalog.InstallParameterGrant(state, parameter)
	} else {
		state.SetDescriptor(addr, next)
	}
	roles := e.RoleStore.Published.Advance()
	databaseRoots := e.Catalog.ProjectDatabasesLocked(state, e.Cluster.Catalogs)
	if err := ctx.Err(); err != nil {
		return err
	}
	if n, ok := next.(*catalog.Relation); ok && n.RelationKind == catalog.SequenceRelation && ref.Address.SubID == 0 {
		stamp, err := e.Catalog.RevisionLocked()
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
	e.Catalog.PublishLocked(state)
	databaseRoots.InstallLocked()
	e.RoleStore.PublishLocked(roles)
	return nil
}
