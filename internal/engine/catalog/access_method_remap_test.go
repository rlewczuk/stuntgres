package catalog

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestAccessMethodPrincipalRemap(t *testing.T) {
	sourceDomain, destinationDomain := &authorization.Domain{}, &authorization.Domain{}
	state := NewState(1)
	InstallBuiltinAccessMethods(state)
	builtin, ok := NewRead(nil, state).AccessMethod(403)
	if !ok {
		t.Fatal("missing builtin method")
	}
	custom := *builtin
	custom.ObjectAddress.ObjectID = 90000
	custom.ObjectName = "remapped_method"
	custom.ObjectRevision = 2
	state.SetDescriptor(custom.Address(), &custom)
	state.BindName(DescriptorNameKey(&custom), custom.Address())
	family := &OperatorFamily{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 90001},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
		ObjectRevision:  2, ObjectName: "remapped_family", Method: custom.Address().ObjectID,
		Owner: sourceDomain.Principal(20),
	}
	state.SetDescriptor(family.Address(), family)
	state.BindName(DescriptorNameKey(family), family.Address())
	next := CloneState(state)
	mappedOwners := 0
	if err := RemapPrincipals(next, PrincipalRemap{
		Principal: func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
			t.Fatal("ownerless method acquired an ordinary principal", ref)
			return authorization.PrincipalRef{}, nil
		},
		SeededOwner: func(ref authorization.PrincipalRef, seeded bool) (authorization.PrincipalRef, error) {
			if seeded || ref != family.Owner {
				t.Fatal("unexpected principal mapping", ref, seeded)
			}
			mappedOwners++
			return destinationDomain.Principal(20), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if mappedOwners != 1 {
		t.Fatal("owning family lost principal remapping", mappedOwners)
	}
	view := NewRead(nil, next)
	for method := range NewRead(nil, state).AccessMethods() {
		if got, ok := view.AccessMethod(method.Address().ObjectID); !ok || got != method || *got != *method {
			t.Fatal("principal remap rewrote ownerless method identity/handler", method, got)
		}
		if got, ok := view.LookupAccessMethod(method.Name()); !ok || got != method {
			t.Fatal("principal remap lost global method name", method)
		}
	}
	mapped, ok := view.OperatorFamily(family.Address())
	if !ok || mapped == family || mapped.Owner != destinationDomain.Principal(20) || mapped.Method != custom.Address().ObjectID || family.Owner != sourceDomain.Principal(20) {
		t.Fatal("owning family remap or source immutability lost", family, mapped)
	}
}
