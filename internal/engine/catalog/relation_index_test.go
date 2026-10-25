package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

// checkRelations requires Relations to hold exactly the *Relation entries of
// the descriptor map, with the same values.
func checkRelations(t *testing.T, s *State, label string) {
	t.Helper()
	want := map[ids.ObjectAddress]*Relation{}
	for a, d := range s.Descriptors() {
		if r, ok := d.(*Relation); ok {
			want[a] = r
		}
	}
	got := map[ids.ObjectAddress]*Relation{}
	for a, r := range s.Relations() {
		if _, dup := got[a]; dup {
			t.Fatalf("%s: Relations visited %v twice", label, a)
		}
		got[a] = r
	}
	if len(got) != len(want) {
		t.Fatalf("%s: Relations has %d entries, want %d", label, len(got), len(want))
	}
	for a, r := range want {
		if got[a] != r {
			t.Fatalf("%s: Relations[%v] = %p, want %p", label, a, got[a], r)
		}
	}
}

func TestCatalogRelationsFollowStateWrites(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	checkRelations(t, base, "fixture")
	added := catalogTestRelation(1001, ns.Address())
	base.SetDescriptor(added.Address(), added)
	checkRelations(t, base, "set")
	// Membership follows the stored value, not the address.
	other := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 1002}, ObjectName: "other", ObjectRevision: 1}
	base.SetDescriptor(added.Address(), other)
	checkRelations(t, base, "relation replaced by a namespace")
	base.SetDescriptor(added.Address(), added)
	checkRelations(t, base, "namespace replaced by a relation")
	published := CloneState(base)
	base.RemoveDescriptor(added.Address())
	checkRelations(t, base, "remove")
	checkRelations(t, published, "copy before remove")
	if _, ok := published.Relation(added.Address()); !ok {
		t.Fatal("a write changed an earlier copy")
	}
	base.ResetDescriptors()
	checkRelations(t, base, "ResetDescriptors")
	if _, ok := published.Relation(rel.Address()); !ok {
		t.Fatal("ResetDescriptors changed a copy")
	}
}

func TestCatalogRelationsFollowBulkLoads(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	base.BeginBulkLoad(16)
	added := catalogTestRelation(2000, ns.Address())
	base.SetDescriptor(added.Address(), added)
	base.RemoveDescriptor(rel.Address())
	checkRelations(t, base, "during a bulk load")
	base.EndBulkLoad()
	checkRelations(t, base, "after a bulk load")
	previous := base.ResetMaps()
	checkRelations(t, base, "ResetMaps")
	base.BeginBulkLoad(16)
	for a, d := range previous {
		base.SetDescriptor(a, d)
	}
	base.EndBulkLoad()
	checkRelations(t, base, "ResetMaps refill")
	if _, ok := base.Relation(added.Address()); !ok {
		t.Fatal("refill lost a relation")
	}
}

func TestCatalogRelationsFollowCandidates(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	kept := catalogTestRelation(3001, ns.Address())
	removed := catalogTestRelation(3002, ns.Address())
	restaged := catalogTestRelation(3003, ns.Address())
	for _, r := range []*Relation{kept, removed, restaged} {
		base.SetDescriptor(r.Address(), r)
		base.BindName(DescriptorNameKey(r), r.Address())
	}
	c := NewCandidate(NewRead(nil, base))
	created := catalogTestRelation(3004, ns.Address())
	if err := c.Create(created); err != nil {
		t.Fatal(err)
	}
	replacement := *rel
	replacement.ObjectRevision++
	if err := c.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	c.RecordRemoval(ids.ObjectRef{Address: removed.Address()}, removed)
	c.RecordRemoval(ids.ObjectRef{Address: restaged.Address()}, restaged)
	rebound := *restaged
	rebound.ObjectRevision++
	c.UpdateDescriptor(&rebound)
	next := c.State()
	checkRelations(t, next, "Candidate.State")
	checkRelations(t, base, "candidate base")
	if r, _ := next.Relation(rel.Address()); r != &replacement {
		t.Fatal("Candidate.State kept the replaced relation")
	}
	if _, ok := next.Relation(removed.Address()); ok {
		t.Fatal("Candidate.State kept a removed relation")
	}
	if r, _ := next.Relation(restaged.Address()); r != &rebound {
		t.Fatal("Candidate.State lost a removed and restaged relation")
	}
	applied := c.ApplyTo(CloneState(base))
	checkRelations(t, applied, "ApplyTo")
}

// TestCatalogRelationsVisitOnceWhileWriting replaces and removes relations of
// the state it iterates. Every relation present at the start is visited once.
func TestCatalogRelationsVisitOnceWhileWriting(t *testing.T) {
	base, ns, _ := catalogReadFixture()
	for oid := ids.OID(1000); oid < 1200; oid++ {
		r := catalogTestRelation(oid, ns.Address())
		base.SetDescriptor(r.Address(), r)
	}
	want := 0
	for range base.Relations() {
		want++
	}
	visits := map[ids.ObjectAddress]int{}
	for address, r := range base.Relations() {
		visits[address]++
		if address.ObjectID%3 == 0 {
			base.RemoveDescriptor(address)
			continue
		}
		next := *r
		next.ObjectRevision++
		base.SetDescriptor(address, &next)
	}
	if len(visits) != want {
		t.Fatalf("visited %d relations, want %d", len(visits), want)
	}
	for address, n := range visits {
		if n != 1 {
			t.Fatalf("visited %v %d times", address, n)
		}
	}
	checkRelations(t, base, "after writes during iteration")
}
