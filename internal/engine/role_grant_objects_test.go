package engine

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestObjectGrantSchemaTypePublication(t *testing.T) {
	for _, kind := range []string{"schema", "type"} {
		t.Run(kind, func(t *testing.T) {
			e, op, reader, _ := objectGrantUnitFixture(t)
			name := "public"
			address := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			want := authorization.UsagePrivilege | authorization.CreatePrivilege
			if kind == "type" {
				name = "int4"
				address = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
				want = authorization.UsagePrivilege
			}
			before, roles := e.catalog.state, e.roles
			original := before.Get(address)
			request := &parser.ObjectGrant{Kind: kind, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}, Privileges: []parser.GrantPrivilege{{All: true}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}, GrantOption: true}
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			next := e.catalog.state.Get(address)
			owner, acl, _ := descriptorACL(next)
			if next == original || e.roles.revision != roles.revision+1 || authorization.ACLRights(acl, owner, []authorization.PrincipalRef{reader}, want, true) != want {
				t.Fatal("wrong immutable class publication", acl)
			}
			if !slices.Contains(descriptorReferences(next), reader) {
				t.Fatal("ACL recipient missing from role dependencies")
			}
			if kind == "schema" && e.catalog.state.NamespaceGeneration() != before.NamespaceGeneration()+1 {
				t.Fatal("schema ACL did not invalidate namespace authority")
			}
			stable, stableRoles := e.catalog.state, e.roles
			request.Relations = append(request.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
			if err := applyObjectGrantUnit(e, op, request); err == nil || e.catalog.state != stable || e.roles != stableRoles {
				t.Fatal("late class target failure published prefix", err)
			}
		})
	}
}

func TestObjectGrantBuiltinTypeAuthority(t *testing.T) {
	e, op, reader, _ := objectGrantUnitFixture(t)
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
	original := e.catalog.state.MustType(address)
	if original.Owner != e.authorizationDomain.Principal(authorization.BootstrapPrincipalID) || original.ACL.Explicit {
		t.Fatal("builtin type has no genuine bootstrap/default authority")
	}
	request := &parser.ObjectGrant{Kind: "type", Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "int4"}}}}, Privileges: []parser.GrantPrivilege{{Name: "usage"}}, Grantees: []parser.GrantGrantee{{Public: true}}, Revoke: true}
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.catalog.state.MustType(address)
	if authorization.ACLRights(next.ACL, next.Owner, []authorization.PrincipalRef{reader}, authorization.UsagePrivilege, false) != 0 || original.ACL.Explicit {
		t.Fatal("builtin PUBLIC revoke changed old root or retained PUBLIC rights")
	}
	broken := *next
	broken.Owner = authorization.PrincipalRef{}
	if err := validateACL(e.roleRead(e.catalog.read(e.catalog.state), e.roles), &broken); err == nil {
		t.Fatal("builtin zero owner bypassed validation")
	}
	request.Relations[0].Parts[0].Text = "_int4"
	before := e.catalog.state
	err := applyObjectGrantUnit(e, op, request)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0LP01" || diagnostic.Hint != "Set the privileges of the element type instead." || e.catalog.state != before {
		t.Fatal("true array ACL mutation accepted or partially published", err)
	}
}
