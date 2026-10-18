package engine

import (
	"runtime"
	"testing"

	"stuntgres/internal/engine/ids"
)

func catalogReadFixture() (*catalogState, *namespaceDescriptor, *relationDescriptor) {
	ns := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}, name: "s", revision: 1}
	rel := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 901}, namespace: ns.address, name: "t", revision: 1}
	s := &catalogState{epoch: 1, descriptors: map[ids.ObjectAddress]Descriptor{}, names: map[ids.NameKey]ids.ObjectAddress{}}
	for _, d := range []Descriptor{ns, rel} {
		s.descriptors[d.Address()] = d
		s.names[descriptorNameKey(d)] = d.Address()
	}
	return s, ns, rel
}

// expectTypeAssertionPanic requires the panic a failed single-value type
// assertion raises, which the Must accessors keep from the map reads they replace.
func expectTypeAssertionPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if _, ok := r.(*runtime.TypeAssertionError); !ok {
			t.Fatalf("%s: got panic %v, want a type assertion panic", name, r)
		}
	}()
	f()
}

func expectNilDereferencePanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		err, ok := recover().(runtime.Error)
		if !ok || err.Error() != "runtime error: invalid memory address or nil pointer dereference" {
			t.Fatalf("%s: got panic %v, want a nil dereference", name, err)
		}
	}()
	f()
}

func TestCatalogReadTypedAccessors(t *testing.T) {
	state, ns, rel := catalogReadFixture()
	read := CatalogRead{state: state}
	missing := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999}

	if got, ok := read.Relation(rel.address); !ok || got != rel {
		t.Fatalf("Relation(present) = %p, %v", got, ok)
	}
	if got, ok := read.Relation(missing); ok || got != nil {
		t.Fatalf("Relation(absent) = %p, %v", got, ok)
	}
	if got, ok := read.Relation(ns.address); ok || got != nil {
		t.Fatalf("Relation(other kind) = %p, %v", got, ok)
	}
	if got, ok := state.Namespace(ns.address); !ok || got != ns {
		t.Fatalf("state Namespace(present) = %p, %v", got, ok)
	}
	if read.MustRelation(rel.address) != rel || state.MustNamespace(ns.address) != ns {
		t.Fatal("Must accessor returned another descriptor")
	}
	expectTypeAssertionPanic(t, "MustRelation(absent)", func() { read.MustRelation(missing) })
	expectTypeAssertionPanic(t, "MustRelation(other kind)", func() { read.MustRelation(ns.address) })
	expectTypeAssertionPanic(t, "MustNamespace(other kind)", func() { state.MustNamespace(rel.address) })
}

func TestCatalogReadUntypedAndNameAccessors(t *testing.T) {
	state, ns, rel := catalogReadFixture()
	read := CatalogRead{state: state}
	missing := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999}

	if read.Get(rel.address) != rel || read.Get(missing) != nil {
		t.Fatal("Get did not mirror the map index")
	}
	if d, ok := read.Find(ns.address); !ok || d != ns {
		t.Fatalf("Find(present) = %v, %v", d, ok)
	}
	if d, ok := read.Find(missing); ok || d != nil {
		t.Fatalf("Find(absent) = %v, %v", d, ok)
	}
	key := descriptorNameKey(rel)
	if a, ok := read.LookupName(key); !ok || a != rel.address || read.NameAddress(key) != rel.address {
		t.Fatalf("LookupName(present) = %v, %v", a, ok)
	}
	absent := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.address, Name: "absent"}
	if a, ok := read.LookupName(absent); ok || a != (ids.ObjectAddress{}) || read.NameAddress(absent) != (ids.ObjectAddress{}) {
		t.Fatalf("LookupName(absent) = %v, %v", a, ok)
	}
	if read.DescriptorCount() != 2 || read.NameCount() != 2 {
		t.Fatalf("counts = %d, %d", read.DescriptorCount(), read.NameCount())
	}
	seen := map[ids.ObjectAddress]Descriptor{}
	for a, d := range read.Descriptors() {
		seen[a] = d
	}
	if len(seen) != 2 || seen[ns.address] != ns || seen[rel.address] != rel {
		t.Fatalf("Descriptors visited %v", seen)
	}
	visits := 0
	for range read.Descriptors() {
		visits++
		break
	}
	names := map[ids.NameKey]ids.ObjectAddress{}
	for k, a := range state.Names() {
		names[k] = a
	}
	if visits != 1 || len(names) != 2 || names[key] != rel.address {
		t.Fatalf("early break visits %d, names %v", visits, names)
	}
}

func TestCatalogReadAccessorsRequirePopulatedRead(t *testing.T) {
	var zero CatalogRead
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1}
	if d, ok := zero.Descriptor(a); ok || d != nil {
		t.Fatal("guarded Descriptor changed on a zero read")
	}
	expectNilDereferencePanic(t, "Relation", func() { zero.Relation(a) })
	expectNilDereferencePanic(t, "MustRelation", func() { zero.MustRelation(a) })
	expectNilDereferencePanic(t, "Get", func() { zero.Get(a) })
	expectNilDereferencePanic(t, "Find", func() { zero.Find(a) })
	expectNilDereferencePanic(t, "NameAddress", func() { zero.NameAddress(ids.NameKey{}) })
	expectNilDereferencePanic(t, "DescriptorCount", func() { zero.DescriptorCount() })
	expectNilDereferencePanic(t, "Descriptors", func() {
		for range zero.Descriptors() {
		}
	})
}
