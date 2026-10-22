package catalog

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestOperatorFamilyPrincipalRemap(t *testing.T) {
	sourceDomain, destinationDomain := &authorization.Domain{}, &authorization.Domain{}
	state := NewState(1)
	InstallBuiltinFamilies(state, sourceDomain.Principal(10))
	base := NewRead(nil, state)
	candidate := NewCandidate(base)
	custom := &OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 90001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, ObjectRevision: 2, ObjectName: "remapped_family", Method: 403, Owner: sourceDomain.Principal(20)}
	if err := candidate.Create(custom); err != nil {
		t.Fatal(err)
	}
	seeded, ordinary := 0, 0
	destination := candidate.State()
	if err := RemapPrincipals(destination, PrincipalRemap{
		Principal: func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
			t.Fatal("family members must not acquire principal authority", ref)
			return authorization.PrincipalRef{}, nil
		},
		SeededOwner: func(ref authorization.PrincipalRef, seed bool) (authorization.PrincipalRef, error) {
			if seed {
				seeded++
				if ref != sourceDomain.Principal(10) {
					t.Fatal("incorrect seed owner", ref)
				}
			} else {
				ordinary++
				if ref != custom.Owner {
					t.Fatal("incorrect custom owner", ref)
				}
			}
			return destinationDomain.Principal(ref.ID() + 100), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if seeded != 49 || ordinary != 1 {
		t.Fatal("missing owner mapping", seeded, ordinary)
	}
	mapped := base.WithState(destination)
	for address, descriptor := range base.Descriptors() {
		switch original := descriptor.(type) {
		case *OperatorFamily:
			got, _ := mapped.OperatorFamily(address)
			want := *original
			want.Owner = destinationDomain.Principal(110)
			if got == original || *got != want || original.Owner != sourceDomain.Principal(10) {
				t.Fatal("seed owner remap mutated source or metadata", original, got)
			}
		case *OperatorFamilyOperator, *OperatorFamilySupport:
			if mapped.Get(address) != descriptor {
				t.Fatal("ownerless member changed during principal remap", address)
			}
		}
	}
	got, _ := mapped.OperatorFamily(custom.Address())
	want := *custom
	want.Owner = destinationDomain.Principal(120)
	if got == custom || *got != want || custom.Owner != sourceDomain.Principal(20) {
		t.Fatal("custom owner remap mutated source or metadata", custom, got)
	}
}

func TestOperatorFamilyAddressRemap(t *testing.T) {
	family := &OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 90001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90002}, ObjectName: "address_family", Method: 783, ObjectRevision: 7}
	operator := &OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: 90003}, ObjectRevision: 8, Family: 90001, Left: 90004, Right: 90005, Operator: 90006, Method: 783, SortFamily: 90007, Number: 1, Purpose: 'o'}
	support := &OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: 90008}, ObjectRevision: 9, Family: 90001, Left: 90004, Right: 90005, Function: 90009, Number: 1}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, address := range []ids.ObjectAddress{family.Address(), family.Namespace(), operator.Address(), support.Address(), {ClassID: ids.TypeClassID, ObjectID: 90004}, {ClassID: ids.TypeClassID, ObjectID: 90005}, {ClassID: ids.OperatorClassID, ObjectID: 90006}, {ClassID: ids.OperatorFamilyClassID, ObjectID: 90007}, {ClassID: ids.ProcedureClassID, ObjectID: 90009}} {
		next := address
		next.ObjectID += 100
		mapping[address] = next
	}
	originalFamily, originalOperator, originalSupport := *family, *operator, *support
	gotFamily := RemapOperatorFamily(family, mapping).(*OperatorFamily)
	gotOperator := RemapOperatorFamily(operator, mapping).(*OperatorFamilyOperator)
	gotSupport := RemapOperatorFamily(support, mapping).(*OperatorFamilySupport)
	wantFamily, wantOperator, wantSupport := originalFamily, originalOperator, originalSupport
	wantFamily.ObjectAddress.ObjectID += 100
	wantFamily.ObjectNamespace.ObjectID += 100
	wantOperator.ObjectAddress.ObjectID += 100
	wantOperator.Family += 100
	wantOperator.Left += 100
	wantOperator.Right += 100
	wantOperator.Operator += 100
	wantOperator.SortFamily += 100
	wantSupport.ObjectAddress.ObjectID += 100
	wantSupport.Family += 100
	wantSupport.Left += 100
	wantSupport.Right += 100
	wantSupport.Function += 100
	if *gotFamily != wantFamily || *gotOperator != wantOperator || *gotSupport != wantSupport {
		t.Fatal("incomplete destination address mapping", gotFamily, gotOperator, gotSupport)
	}
	if *family != originalFamily || *operator != originalOperator || *support != originalSupport {
		t.Fatal("address remap mutated source")
	}
	for _, descriptor := range []Descriptor{family, operator, support} {
		if RemapOperatorFamily(descriptor, nil) != descriptor {
			t.Fatal("unchanged address mapping copied immutable descriptor")
		}
	}
}
