package rolecmds_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
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
			before, roles := e.Catalog.State, e.RoleStore.Published
			original := before.Get(address)
			request := &parser.ObjectGrant{Kind: kind, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}, Privileges: []parser.GrantPrivilege{{All: true}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}, GrantOption: true}
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			next := e.Catalog.State.Get(address)
			owner, acl, _ := access.DescriptorACL(next)
			if next == original || e.RoleStore.Published.Revision != roles.Revision+1 || authorization.ACLRights(acl, owner, []authorization.PrincipalRef{reader}, want, true) != want {
				t.Fatal("wrong immutable class publication", acl)
			}
			if !slices.Contains(descriptorReferences(next), reader) {
				t.Fatal("ACL recipient missing from role dependencies")
			}
			if kind == "schema" && e.Catalog.State.NamespaceGeneration() != before.NamespaceGeneration()+1 {
				t.Fatal("schema ACL did not invalidate namespace authority")
			}
			stable, stableRoles := e.Catalog.State, e.RoleStore.Published
			request.Relations = append(request.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
			if err := applyObjectGrantUnit(e, op, request); err == nil || e.Catalog.State != stable || e.RoleStore.Published != stableRoles {
				t.Fatal("late class target failure published prefix", err)
			}
		})
	}
}

func TestObjectGrantBuiltinTypeAuthority(t *testing.T) {
	e, op, reader, _ := objectGrantUnitFixture(t)
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
	original := e.Catalog.State.MustType(address)
	if original.Owner != e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID) || original.ACL.Explicit {
		t.Fatal("builtin type has no genuine bootstrap/default authority")
	}
	request := &parser.ObjectGrant{Kind: "type", Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "int4"}}}}, Privileges: []parser.GrantPrivilege{{Name: "usage"}}, Grantees: []parser.GrantGrantee{{Public: true}}, Revoke: true}
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.Catalog.State.MustType(address)
	if authorization.ACLRights(next.ACL, next.Owner, []authorization.PrincipalRef{reader}, authorization.UsagePrivilege, false) != 0 || original.ACL.Explicit {
		t.Fatal("builtin PUBLIC revoke changed old root or retained PUBLIC rights")
	}
	broken := *next
	broken.Owner = authorization.PrincipalRef{}
	if err := access.ValidateACL(e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), &broken); err == nil {
		t.Fatal("builtin zero owner bypassed validation")
	}
	request.Relations[0].Parts[0].Text = "_int4"
	before := e.Catalog.State
	err := applyObjectGrantUnit(e, op, request)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0LP01" || diagnostic.Hint != "Set the privileges of the element type instead." || e.Catalog.State != before {
		t.Fatal("true array ACL mutation accepted or partially published", err)
	}
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
