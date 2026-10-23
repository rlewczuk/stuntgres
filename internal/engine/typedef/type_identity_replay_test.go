package typedef

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTypeLifecycleInstalledReplay(t *testing.T) {
	r := NewDomainReplayNames()
	source := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	destination := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}
	key := func(namespace ids.ObjectAddress, name string) ids.NameKey {
		return ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: name}
	}
	// Existing manifest roles provide these captured identities; the predictor
	// neither allocates destination OIDs nor creates executable descriptors.
	if err := r.ReserveIdentity(90001, key(source, "cube")); err != nil {
		t.Fatal(err)
	}
	r.AssociateIdentity(90001, 90002)
	if err := r.ReserveIdentity(90003, key(source, "citext")); err != nil {
		t.Fatal(err)
	}
	r.AssociateIdentity(90003, 90004)
	for _, rename := range []struct {
		id   sqltype.ID
		name string
	}{{90001, "parked_cube"}, {90003, "parked_citext"}, {90001, "citext"}, {90003, "cube"}} {
		// The stable role identity, not the final spelling, selects each move.
		if err := r.Relocate(rename.id, key(source, rename.name), false); err != nil {
			t.Fatal(err)
		}
	}
	if r.Names[90001] != key(source, "citext") || r.Names[90002] != key(source, "_citext") || r.Names[90003] != key(source, "cube") || r.Names[90004] != key(source, "_cube") {
		t.Fatal("cross-role swap lost companion identity")
	}
	// A genuine declaration displaces the generated array; consumers must use
	// the simulated array name, never a string guessed from the parent.
	if err := r.ReserveIdentity(90005, key(source, "_cube")); err != nil {
		t.Fatal(err)
	}
	r.AssociateIdentity(90005, 90006)
	if r.Names[90004] != key(source, "__cube") {
		t.Fatal("generated array displacement lost")
	}
	if err := r.Relocate(90003, key(destination, "cube"), true); err != nil {
		t.Fatal(err)
	}
	if r.Names[90004] != key(destination, "__cube") {
		t.Fatal("schema move regenerated displaced spelling")
	}
	if err := r.ReserveIdentity(90007, key(source, "occupied")); err != nil {
		t.Fatal(err)
	}
	before := r.Names[90003]
	if err := r.Relocate(90003, key(source, "occupied"), true); err == nil || r.Names[90003] != before {
		t.Fatal("failed move changed predicted identity")
	}
	if err := r.Relocate(90003, key(destination, "_cube"), false); err != nil {
		t.Fatal(err)
	}
	if r.Names[90003] != key(destination, "_cube") || r.Names[90004] != key(destination, "__cube") {
		t.Fatal("rename lost actual companion")
	}
}
