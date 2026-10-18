package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestCatalogStateWritesStayOnTheirState(t *testing.T) {
	base, ns, rel := catalogReadFixture()

	copied := *base
	copied.cloneDescriptors()
	copied.removeDescriptor(rel.address)
	if _, ok := base.Relation(rel.address); !ok {
		t.Fatal("removal from a cloned state changed its source")
	}
	if _, ok := copied.Find(rel.address); ok {
		t.Fatal("removal did not apply to the clone")
	}

	other := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 902}, namespace: ns.address, name: "u", revision: 1}
	copied.setDescriptor(other.address, other)
	if base.Get(other.address) != nil || copied.Get(other.address) != other {
		t.Fatal("setDescriptor did not stay on the clone")
	}

	key := descriptorNameKey(rel)
	copied.unbindName(key)
	if _, ok := copied.LookupName(key); ok {
		t.Fatal("unbindName kept the binding")
	}
	copied.bindName(key, other.address)
	if copied.NameAddress(key) != other.address {
		t.Fatal("bindName did not bind")
	}

	previous := copied.resetMaps(4)
	if copied.DescriptorCount() != 0 || copied.NameCount() != 0 {
		t.Fatal("resetMaps left entries")
	}
	if previous[other.address] != other || previous[ns.address] != ns {
		t.Fatal("resetMaps lost the previous descriptors")
	}
	if base.DescriptorCount() != 2 {
		t.Fatal("resetMaps changed the source state")
	}
}
