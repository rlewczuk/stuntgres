package catalog

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

	other := &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 902}, ObjectNamespace: ns.Address(), ObjectName: "u", ObjectRevision: 1}
	copied.SetDescriptor(other.Address(), other)
	if base.Get(other.Address()) != nil || copied.Get(other.Address()) != other {
		t.Fatal("setDescriptor did not stay on the clone")
	}

	key := DescriptorNameKey(rel)
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

func TestCatalogCopiesStayIndependent(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	other := &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 902}, ObjectNamespace: ns.Address(), ObjectName: "u", ObjectRevision: 1}

	clone := CloneState(base)
	clone.RemoveDescriptor(rel.Address())
	clone.UnbindName(DescriptorNameKey(rel))
	clone.SetDescriptor(other.Address(), other)
	clone.BindName(DescriptorNameKey(other), other.Address())
	if base.Get(rel.Address()) != rel || base.NameAddress(DescriptorNameKey(rel)) != rel.Address() || base.Get(other.Address()) != nil || base.DescriptorCount() != 2 || base.NameCount() != 2 {
		t.Fatal("writes to a cloned state changed its source")
	}
	if clone.Get(ns.Address()) != ns || clone.Get(other.Address()) != other || clone.Get(rel.Address()) != nil {
		t.Fatal("cloned state lost its own writes or source entries")
	}

	candidate := NewCandidate(Read{state: base})
	if err := candidate.Create(other); err != nil {
		t.Fatal(err)
	}
	candidate.RecordRemoval(ids.ObjectRef{Address: rel.Address()}, rel)
	next := candidate.State()
	if next.Get(other.Address()) != other || next.NameAddress(DescriptorNameKey(other)) != other.Address() || next.Get(ns.Address()) != ns {
		t.Fatal("candidate state lost staged or base entries")
	}
	if _, ok := next.Find(rel.Address()); ok || next.NameAddress(DescriptorNameKey(rel)) != (ids.ObjectAddress{}) {
		t.Fatal("candidate state kept a staged removal")
	}
	next.SetDescriptor(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 903}, other)
	if base.Get(other.Address()) != nil || base.Get(rel.Address()) != rel || base.DescriptorCount() != 2 {
		t.Fatal("candidate state shares maps with its base")
	}

	// States without maps still yield writable copies.
	empty := &State{epoch: 1}
	for name, copied := range map[string]*State{"clone": CloneState(empty), "candidate": (&Candidate{Base: Read{state: empty}}).State()} {
		copied.SetDescriptor(other.Address(), other)
		copied.BindName(DescriptorNameKey(other), other.Address())
		if copied.Get(other.Address()) != other || copied.NameAddress(DescriptorNameKey(other)) != other.Address() {
			t.Fatalf("%s of a state without maps is not writable", name)
		}
	}
}
