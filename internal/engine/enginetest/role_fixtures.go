package enginetest

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func AclUnitRoles(e *core.Engine, names ...string) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	next := CloneRoleFixture(e.RoleStore.Published)
	for i, name := range names {
		PutFixtureRole(next, authorization.RoleDescriptor{Ref: e.RoleStore.Domain.Principal(uint64(20000 + i)), Name: name, Inherit: true, ConnectionLimit: -1})
	}
	e.RoleStore.PublishLocked(next)
	if nextID := uint64(20000 + len(names)); e.RoleStore.NextPrincipal() < nextID {
		e.RoleStore.InstallPrincipalFrontierLocked(nextID)
	}
}

func FixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}

func FixtureEdges(r *access.RoleRecord) []authorization.Membership {
	return r.Root.Read(authorization.PrincipalRef{}).Memberships()
}

// Build private graph fixtures through genuine storage and membership candidates.
// Temporary descriptor names/flags exist only on unpublished roots and are restored.
func SetFixtureEdges(r *access.RoleRecord, edges []authorization.Membership) {
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
		original := FixtureRole(r, edge.Grantor)
		oldBootstrap, hasBootstrap := r.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			temp := oldBootstrap
			temp.Name = fmt.Sprintf("fixture_bootstrap_%d", temp.Ref.ID())
			PutFixtureRole(r, temp)
		}
		elevated := original
		elevated.Name = "postgres"
		elevated.Superuser = true
		PutFixtureRole(r, elevated)
		target := FixtureRole(r, edge.Role)
		member := FixtureRole(r, edge.Member)
		request := &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: target.Name}}, Members: []parser.RoleSpec{{Name: member.Name}}, Options: []parser.MembershipOption{{Name: "admin", Value: edge.Admin}, {Name: "inherit", Value: edge.Inherit}, {Name: "set", Value: edge.Set}}}
		root, changed, _, err := authorization.MembershipCandidate(context.Background(), r.Root.Read(authorization.PrincipalRef{}), authorization.RoleState{CurrentUser: edge.Grantor, SessionUser: edge.Grantor}, request, nil, false, false)
		if err != nil {
			panic(err)
		}
		if changed {
			r.Root = root
		}
		PutFixtureRole(r, original)
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			PutFixtureRole(r, oldBootstrap)
		}
	}
	destination.Root = candidate.Root
}

func DefaultUnitStatement(class, privilege string, creators, schemas, grantees []string) *parser.AlterDefaultPrivileges {
	s := &parser.AlterDefaultPrivileges{Class: class, Privileges: []parser.GrantPrivilege{{Name: privilege, All: privilege == "all"}}}
	if creators != nil {
		option := parser.DefaultPrivilegesOption{Roles: make([]parser.RoleSpec, len(creators))}
		for i, name := range creators {
			option.Roles[i] = parser.RoleSpec{Name: name}
		}
		s.Options = append(s.Options, option)
	}
	if schemas != nil {
		option := parser.DefaultPrivilegesOption{Schemas: make([]parser.Identifier, len(schemas))}
		for i, name := range schemas {
			option.Schemas[i] = parser.Identifier{Text: name}
		}
		s.Options = append(s.Options, option)
	}
	for _, name := range grantees {
		s.Grantees = append(s.Grantees, parser.GrantGrantee{Public: name == "PUBLIC", Role: parser.RoleSpec{Name: name}})
	}
	return s
}

func NamespaceCapturedAuthorization(t testing.TB, s *session.Session) core.AuthorizationView {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func CloneRoleFixture(r *access.RoleRecord) *access.RoleRecord {
	return &access.RoleRecord{Root: r.Root, Revision: r.Revision + 1}
}

func PutFixtureRole(r *access.RoleRecord, d authorization.RoleDescriptor) {
	root, err := r.Root.PutDescriptor(d)
	if err != nil {
		panic(err)
	}
	r.Root = root
}
