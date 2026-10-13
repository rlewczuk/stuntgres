package authorization

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRoleAttributesDirectAuthority(t *testing.T) {
	domain := &Domain{}
	root := Seed(domain)
	bootstrap := domain.Principal(10)
	creator := domain.Principal(100)
	member := domain.Principal(101)
	target := domain.Principal(102)
	root.roles[creator] = RoleDescriptor{Ref: creator, Name: "creator", CreateRole: true, Inherit: true, ConnectionLimit: -1}
	root.roles[member] = RoleDescriptor{Ref: member, Name: "member", Inherit: true, ConnectionLimit: -1}
	root.roles[target] = RoleDescriptor{Ref: target, Name: "target", Inherit: true, ConnectionLimit: -1}
	for ref, d := range root.roles {
		root.names[d.Name] = ref
	}
	root.edges = append(root.edges, Membership{Role: creator, Member: member, Grantor: bootstrap, Inherit: true, Set: true}, Membership{Role: target, Member: creator, Grantor: bootstrap, Admin: true})
	read := root.Read(bootstrap)
	create := &parser.CreateRole{Name: parser.Identifier{Text: "new_role"}}
	_, d, _, err := ValidateRoleAttributes(read, RoleState{CurrentUser: creator}, create)
	if err != nil || d.Login || !d.Inherit || d.ConnectionLimit != -1 {
		t.Fatal("direct CREATE defaults", d, err)
	}
	_, _, _, err = ValidateRoleAttributes(read, RoleState{CurrentUser: member}, create)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Detail != "Only roles with the CREATEROLE attribute may create roles." {
		t.Fatal("special attributes inherited", err)
	}
	alter := &parser.AlterRole{Name: parser.Identifier{Text: "target"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}
	if _, _, _, err := ValidateRoleAttributes(read, RoleState{CurrentUser: creator}, alter); err != nil {
		t.Fatal("direct create/admin authority", err)
	}
	if _, _, _, err := ValidateRoleAttributes(read, RoleState{CurrentUser: member}, alter); err == nil {
		t.Fatal("inherited CREATEROLE shortcut")
	}
	changed := ApplyRoleOptions(root.roles[target], alter.Options)
	next, _, err := AttributeCandidate(context.Background(), read, RoleState{CurrentUser: creator}, root.roles[creator], changed, nil, nil, false, false)
	if err != nil || !next.roles[target].Login || read.state.roles[target].Login {
		t.Fatal("prior attribute root mutated", err)
	}
}
