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

func cloneRoleFixture(r *roleCatalog) *roleCatalog {
	return &roleCatalog{Root: r.Root, Revision: r.Revision + 1}
}
func emptyRoleFixture(e *Engine) *roleCatalog {
	root := authorization.Seed(&e.roles.domain)
	targets := make(map[authorization.PrincipalRef]authorization.RoleDescriptor)
	for ref, d := range root.Read(authorization.PrincipalRef{}).RoleEntries() {
		targets[ref] = d
	}
	return &roleCatalog{Root: root.Retire(targets), Revision: 1}
}
func fixtureRole(r *roleCatalog, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}
func putFixtureRole(r *roleCatalog, d authorization.RoleDescriptor) {
	root, err := r.Root.PutDescriptor(d)
	if err != nil {
		panic(err)
	}
	r.Root = root
}
func retireFixtureRole(r *roleCatalog, ref authorization.PrincipalRef) {
	d := fixtureRole(r, ref)
	r.Root = r.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{ref: d})
}
func fixtureEdges(r *roleCatalog) []authorization.Membership {
	return r.Root.Read(authorization.PrincipalRef{}).Memberships()
}

// Build private graph fixtures through genuine storage and membership candidates.
// Temporary descriptor names/flags exist only on unpublished roots and are restored.
func setFixtureEdges(r *roleCatalog, edges []authorization.Membership) {
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
