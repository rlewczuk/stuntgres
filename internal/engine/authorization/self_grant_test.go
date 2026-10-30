package authorization

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
)

func TestCreateroleSelfGrantCandidateOwnership(t *testing.T) {
	domain := &Domain{}
	root := Seed(domain)
	bootstrap, creator, child := domain.Principal(10), domain.Principal(100), domain.Principal(101)
	actor := RoleDescriptor{Ref: creator, Name: "creator", CreateRole: true, ConnectionLimit: -1}
	var err error
	root, err = root.PutDescriptor(actor)
	if err != nil {
		t.Fatal(err)
	}
	originalEdges := append([]Membership(nil), root.edges...)
	originalRoles, originalNames := maps.Clone(root.roles), maps.Clone(root.names)
	read := root.Read(bootstrap)
	state := RoleState{CurrentUser: creator}
	d := RoleDescriptor{Ref: child, Name: "child", Inherit: true, ConnectionLimit: -1}
	create := &parser.CreateRole{Name: parser.Identifier{Text: "child"}}
	for _, options := range []CreatorSelfGrant{{}, {Set: true}, {Inherit: true}, {Set: true, Inherit: true}} {
		next, _, err := AttributeCandidate(t.Context(), read, state, actor, d, create, options, nil, false, false)
		if err != nil {
			t.Fatal(err)
		}
		want := append([]Membership(nil), originalEdges...)
		want = append(want, Membership{Role: child, Member: creator, Grantor: bootstrap, Admin: true})
		if options.Set || options.Inherit {
			want = append(want, Membership{Role: child, Member: creator, Grantor: creator, Set: options.Set, Inherit: options.Inherit})
		}
		if !reflect.DeepEqual(next.edges, want) || !reflect.DeepEqual(root.edges, originalEdges) || !reflect.DeepEqual(read.state.edges, originalEdges) {
			t.Fatal("origin/flags/old backing", next.edges, want, root.edges)
		}
		if len(originalEdges) != 0 && &next.edges[0] == &root.edges[0] {
			t.Fatal("candidate borrowed original edge backing")
		}
		if _, ok := read.state.names["child"]; ok {
			t.Fatal("descriptor leaked into held root")
		}
	}
	admin := root.roles[bootstrap]
	next, _, err := AttributeCandidate(t.Context(), read, RoleState{CurrentUser: bootstrap}, admin, d, create, CreatorSelfGrant{Set: true, Inherit: true}, nil, false, false)
	if err != nil || !reflect.DeepEqual(next.edges, originalEdges) {
		t.Fatal("superuser got creator membership", next.edges, err)
	}
	// A later initial clause failure discards descriptor and BOTH grant origins.
	create.Options.InitialMemberships = []parser.InitialRoleMembership{{Kind: "role", Members: []parser.RoleSpec{{Name: "absent"}}}}
	if _, _, err := AttributeCandidate(t.Context(), read, state, actor, d, create, CreatorSelfGrant{Set: true}, nil, false, false); err == nil {
		t.Fatal("missing initial member accepted")
	}
	create.Options.InitialMemberships[0].Members[0].Name = "creator"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := AttributeCandidate(ctx, read, state, actor, d, create, CreatorSelfGrant{Inherit: true}, nil, false, false); err == nil {
		t.Fatal("cancelled initial candidate accepted")
	}
	if !reflect.DeepEqual(root.edges, originalEdges) || !reflect.DeepEqual(read.state.edges, originalEdges) ||
		!reflect.DeepEqual(root.roles, originalRoles) || !reflect.DeepEqual(root.names, originalNames) ||
		!reflect.DeepEqual(read.state.roles, originalRoles) || !reflect.DeepEqual(read.state.names, originalNames) {
		t.Fatal("rejected candidate changed prior root")
	}
}
