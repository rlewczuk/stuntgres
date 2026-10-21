package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"testing"
)

func TestLargeObjectDescriptorAuthority(t *testing.T) {
	domain := &authorization.Domain{}
	owner := &Owner{Principals: domain}
	state, _, relation := catalogReadFixture()
	content, err := (largeobject.Value{}).Write(4294967296, "\x00\xff\x00")
	if err != nil {
		t.Fatal(err)
	}
	d := &LargeObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: relation.Address().ObjectID}, ObjectName: "901", ObjectRevision: 2, Owner: domain.Principal(10), Content: content}
	state.SetDescriptor(d.Address(), d)
	state.BindName(DescriptorNameKey(d), d.Address())
	view := Read{owner: owner, state: state}
	provider := LargeObjectProvider{}
	if err := provider.Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if state.Get(relation.Address()) != relation || state.Get(d.Address()) != d {
		t.Fatal("class-local identity collision")
	}
	// Fixed LO OIDs may also equal an embedded definition in another class.
	withCheck := *relation
	withCheck.Constraints.Check = []CheckConstraint{{OID: 910009}}
	state.SetDescriptor(relation.Address(), &withCheck)
	definitionCollision := *d
	definitionCollision.ObjectAddress.ObjectID = 910009
	definitionCollision.ObjectName = "910009"
	state.SetDescriptor(definitionCollision.Address(), &definitionCollision)
	state.BindName(DescriptorNameKey(&definitionCollision), definitionCollision.Address())
	if err := ValidateDefinitionOIDs(view); err != nil {
		t.Fatal("class-local definition collision", err)
	}
	copied := *d
	if err := provider.Validate(view, &copied); err == nil {
		t.Fatal("uninstalled descriptor accepted")
	}
	if err := provider.Validate(Read{}, d); err == nil {
		t.Fatal("missing catalog accepted")
	}
	foreign := *d
	foreign.Owner = (&authorization.Domain{}).Principal(10)
	state.SetDescriptor(d.Address(), &foreign)
	if err := provider.Validate(view, &foreign); err == nil {
		t.Fatal("foreign owner accepted")
	}
}

func TestLargeObjectPrincipalRemap(t *testing.T) {
	oldDomain, nextDomain := &authorization.Domain{}, &authorization.Domain{}
	oldOwner, oldReader := oldDomain.Principal(10), oldDomain.Principal(20)
	state := &State{epoch: 1, descriptors: map[ids.ObjectAddress]Descriptor{}, names: map[ids.NameKey]ids.ObjectAddress{}}
	content, err := (largeobject.Value{}).Write(4294967296, "\x00z\x00")
	if err != nil {
		t.Fatal(err)
	}
	d := &LargeObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 910001}, ObjectName: "910001", ObjectRevision: 1, Owner: oldOwner, Content: content, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: oldReader, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege},
		{Grantor: oldOwner, Grantee: authorization.ACLGrantee{Role: oldReader}, Privileges: authorization.UpdatePrivilege, GrantOptions: authorization.UpdatePrivilege},
	}}}
	state.SetDescriptor(d.Address(), d)
	state.BindName(DescriptorNameKey(d), d.Address())
	if err := RemapPrincipals(state, PrincipalRemap{Principal: func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		return nextDomain.Principal(ref.ID() + 100), nil
	}}); err != nil {
		t.Fatal(err)
	}
	n := state.Get(d.Address()).(*LargeObject)
	if n == d || n.Owner != nextDomain.Principal(110) || len(n.ACL.Entries) != 2 || n.ACL.Entries[0].Grantor != nextDomain.Principal(120) || !n.ACL.Entries[0].Grantee.Public || n.ACL.Entries[1].Grantee.Role != nextDomain.Principal(120) || n.ACL.Entries[1].GrantOptions != authorization.UpdatePrivilege {
		t.Fatal("owner/raw ordered ACL remap", n)
	}
	if d.Owner != oldOwner || d.ACL.Entries[0].Grantor != oldReader {
		t.Fatal("published source mutated")
	}
	if got, err := n.Content.Read(4294967295, 4); err != nil || got != "\x00\x00z\x00" {
		t.Fatalf("sparse content remap%x,%v", got, err)
	}
}
