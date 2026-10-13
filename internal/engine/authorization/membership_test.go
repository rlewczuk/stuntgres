package authorization

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestMembershipCandidateImmutabilityAndReachedNotices(t *testing.T) {
	domain := &Domain{}
	root := Seed(domain)
	bootstrap := domain.Principal(10)
	role := domain.Principal(100)
	member := domain.Principal(101)
	for ref, d := range map[PrincipalRef]RoleDescriptor{role: {Ref: role, Name: "role", Inherit: true}, member: {Ref: member, Name: "member", Inherit: true}} {
		root.roles[ref] = d
		root.names[d.Name] = ref
	}
	state := RoleState{CurrentUser: bootstrap, SessionUser: bootstrap}
	read := root.Read(bootstrap)
	request := &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: "role"}}, Members: []parser.RoleSpec{{Name: "member"}}}
	next, changed, notices, err := MembershipCandidate(context.Background(), read, state, request, nil, true, true)
	if err != nil || !changed || len(next.edges) != 4 || len(root.edges) != 3 || len(notices) != 0 || next.edges[3] != (Membership{Role: role, Member: member, Grantor: bootstrap, Inherit: true, Set: true}) {
		t.Fatal(next.edges, changed, notices, err)
	}
	retained := next.Read(bootstrap)
	request.Targets = append(request.Targets, parser.RoleSpec{Name: "missing"})
	_, _, notices, err = MembershipCandidate(context.Background(), retained, state, request, nil, true, true)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || len(notices) != 1 || notices[0].Severity != "NOTICE" || notices[0].Message != "role \"member\" has already been granted membership in role \"role\" by role \"postgres\"" {
		t.Fatal("reached notice lost", notices, err)
	}
	if !reflect.DeepEqual(retained.Memberships(), next.edges) {
		t.Fatal("failed prefix changed retained root")
	}
	request.Targets = []parser.RoleSpec{{Name: "role"}, {Name: "role"}}
	request.Revoke = true
	_, _, _, err = MembershipCandidate(context.Background(), retained, state, request, nil, true, true)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "tuple already updated by self" || len(retained.Memberships()) != 4 {
		t.Fatal("original row actions", err)
	}
}
