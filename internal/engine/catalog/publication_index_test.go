package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func checkPublicationIndexes(t *testing.T, state *State, label string) {
	t.Helper()
	want := make(map[ids.ObjectAddress]Descriptor)
	for address, d := range state.Descriptors() {
		switch d.(type) {
		case *Publication, *PublicationRelation, *PublicationNamespace:
			want[address] = d
		}
	}
	got := make(map[ids.ObjectAddress]Descriptor)
	add := func(address ids.ObjectAddress, d Descriptor) {
		if got[address] != nil {
			t.Fatalf("%s: duplicate publication index entry %v", label, address)
		}
		got[address] = d
	}
	read := NewRead(nil, state)
	for a, d := range read.Publications() {
		add(a, d)
	}
	for a, d := range read.PublicationRelations() {
		add(a, d)
	}
	for a, d := range read.PublicationNamespaces() {
		add(a, d)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: publication indexes have %d entries, want %d", label, len(got), len(want))
	}
	for address, d := range want {
		if got[address] != d {
			t.Fatalf("%s: publication index lost descriptor backing %v", label, address)
		}
	}
}

func TestCatalogPublicationIndexLifecycle(t *testing.T) {
	base, _, relation := catalogReadFixture()
	p := &Publication{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationClassID, ObjectID: 30001}, ObjectRevision: 1, ObjectName: "indexed", GeneratedColumns: 'n'}
	m := &PublicationRelation{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationRelationClassID, ObjectID: 30002}, ObjectRevision: 1, Publication: p.Address(), Relation: relation.Address()}
	n := &PublicationNamespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationNamespaceClassID, ObjectID: 30003}, ObjectRevision: 1, Publication: p.Address(), Schema: relation.Namespace()}
	for _, d := range []Descriptor{p, m, n} {
		base.SetDescriptor(d.Address(), d)
		base.BindName(DescriptorNameKey(d), d.Address())
	}
	clone := CloneState(base)
	checkPublicationIndexes(t, clone, "clone")
	unrelated := NewCandidate(NewRead(nil, clone))
	nextRelation := *relation
	nextRelation.ObjectRevision++
	if err := unrelated.Replace(&nextRelation); err != nil {
		t.Fatal(err)
	}
	projected := unrelated.State()
	checkPublicationIndexes(t, projected, "unrelated candidate")
	published := unrelated.ApplyTo(base)
	checkPublicationIndexes(t, published, "unrelated publication")
	candidate := NewCandidate(NewRead(nil, published))
	replacement := *p
	replacement.ObjectName = "renamed"
	replacement.ObjectRevision++
	if err := candidate.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	candidate.RecordRemoval(ids.ObjectRef{Address: m.Address()}, m)
	checkPublicationIndexes(t, candidate.State(), "replacement/removal candidate")
	applied := candidate.ApplyTo(published)
	checkPublicationIndexes(t, applied, "replacement/removal publication")
	if applied.Get(p.Address()) != &replacement || applied.Get(m.Address()) != nil {
		t.Fatal("actual publication mutation not applied")
	}
	bulk := CloneState(applied)
	bulk.BeginBulkLoad(0)
	bulk.RemoveDescriptor(n.Address())
	bulk.EndBulkLoad()
	checkPublicationIndexes(t, bulk, "bulk removal")
	reset := CloneState(bulk)
	reset.ResetDescriptors()
	checkPublicationIndexes(t, reset, "reset descriptors")
	reset = CloneState(bulk)
	_ = reset.ResetMaps()
	checkPublicationIndexes(t, reset, "reset maps")
	for _, snapshot := range []*State{base, clone, projected, published} {
		checkPublicationIndexes(t, snapshot, "retained root")
		if snapshot.Get(p.Address()) != p || snapshot.Get(m.Address()) != m || snapshot.Get(n.Address()) != n {
			t.Fatal("new index versions mutated retained descriptors")
		}
	}
}
