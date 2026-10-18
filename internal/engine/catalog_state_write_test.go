package engine

import (
	"maps"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestCatalogStateWritesStayOnTheirState(t *testing.T) {
	base, ns, rel := catalogReadFixture()

	copied := *base
	copied.CloneDescriptors()
	copied.RemoveDescriptor(rel.Address())
	if _, ok := base.Relation(rel.Address()); !ok {
		t.Fatal("removal from a cloned state changed its source")
	}
	if _, ok := copied.Find(rel.Address()); ok {
		t.Fatal("removal did not apply to the clone")
	}

	other := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 902}, ObjectNamespace: ns.Address(), ObjectName: "u", ObjectRevision: 1}
	copied.SetDescriptor(other.Address(), other)
	if base.Get(other.Address()) != nil || copied.Get(other.Address()) != other {
		t.Fatal("setDescriptor did not stay on the clone")
	}

	key := descriptorNameKey(rel)
	copied.UnbindName(key)
	if _, ok := copied.LookupName(key); ok {
		t.Fatal("unbindName kept the binding")
	}
	copied.BindName(key, other.Address())
	if copied.NameAddress(key) != other.Address() {
		t.Fatal("bindName did not bind")
	}

	previous := maps.Collect(copied.ResetMaps(4))
	if copied.DescriptorCount() != 0 || copied.NameCount() != 0 {
		t.Fatal("resetMaps left entries")
	}
	if previous[other.Address()] != other || previous[ns.Address()] != ns {
		t.Fatal("resetMaps lost the previous descriptors")
	}
	if base.DescriptorCount() != 2 {
		t.Fatal("resetMaps changed the source state")
	}
}
