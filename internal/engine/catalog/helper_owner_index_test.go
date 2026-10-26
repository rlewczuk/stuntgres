package catalog

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine/ids"
)

// helperTestImplementation stands in for an extension provider; the index
// reads only whether a procedure has one.
type helperTestImplementation struct{}

func (helperTestImplementation) Control() ExtensionControl                { return ExtensionControl{} }
func (helperTestImplementation) Versions() ExtensionVersions              { return ExtensionVersions{} }
func (helperTestImplementation) ValidateExtension(Read, *Extension) error { return nil }

func helperTestOwner(oid ids.OID, ns ids.ObjectAddress) *Procedure {
	return &Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ns,
		ObjectName: fmt.Sprintf("agg%d", oid), ObjectRevision: 1, Implementation: helperTestImplementation{},
		Aggregate: &OwnedAggregate{Helpers: [2]RoutineIdentity{{oid + 1, ns.ObjectID, "agg_state", "2281,23"}, {oid + 2, ns.ObjectID, "agg_final", "2281"}}},
	}
}

// checkHelperOwners requires HelperOwners to hold exactly the procedures with
// both Aggregate and Implementation set, with the same values.
func checkHelperOwners(t *testing.T, s *State, label string) {
	t.Helper()
	want := map[ids.ObjectAddress]*Procedure{}
	for a, d := range s.Descriptors() {
		if p, ok := d.(*Procedure); ok && p.Aggregate != nil && p.Implementation != nil {
			want[a] = p
		}
	}
	got := map[ids.ObjectAddress]*Procedure{}
	for a, p := range NewRead(nil, s).HelperOwners() {
		if _, dup := got[a]; dup {
			t.Fatalf("%s: HelperOwners visited %v twice", label, a)
		}
		got[a] = p
	}
	if len(got) != len(want) {
		t.Fatalf("%s: HelperOwners has %d entries, want %d", label, len(got), len(want))
	}
	for a, p := range want {
		if got[a] != p {
			t.Fatalf("%s: HelperOwners[%v] = %p, want %p", label, a, got[a], p)
		}
	}
}

func TestCatalogHelperOwnersFollowStateWrites(t *testing.T) {
	base, ns, _ := catalogReadFixture()
	checkHelperOwners(t, base, "fixture")
	owner := helperTestOwner(1001, ns.Address())
	base.SetDescriptor(owner.Address(), owner)
	checkHelperOwners(t, base, "set")
	// An ordinary aggregate or a provider routine without helpers is not an owner.
	plain := *owner
	plain.Implementation = nil
	base.SetDescriptor(owner.Address(), &plain)
	checkHelperOwners(t, base, "owner replaced by an ordinary aggregate")
	provider := *owner
	provider.Aggregate = nil
	base.SetDescriptor(owner.Address(), &provider)
	checkHelperOwners(t, base, "owner replaced by a provider routine")
	base.SetDescriptor(owner.Address(), catalogTestRelation(1001, ns.Address()))
	checkHelperOwners(t, base, "owner replaced by a relation")
	base.SetDescriptor(owner.Address(), owner)
	checkHelperOwners(t, base, "relation replaced by an owner")
	published := CloneState(base)
	base.RemoveDescriptor(owner.Address())
	checkHelperOwners(t, base, "remove")
	checkHelperOwners(t, published, "copy before remove")
	if _, ok := published.Procedure(owner.Address()); !ok {
		t.Fatal("a write changed an earlier copy")
	}
	published.ResetDescriptors()
	checkHelperOwners(t, published, "ResetDescriptors")
}

func TestCatalogHelperOwnersFollowBulkLoads(t *testing.T) {
	base, ns, _ := catalogReadFixture()
	kept := helperTestOwner(2000, ns.Address())
	base.SetDescriptor(kept.Address(), kept)
	base.BeginBulkLoad(16)
	added := helperTestOwner(2010, ns.Address())
	base.SetDescriptor(added.Address(), added)
	base.RemoveDescriptor(kept.Address())
	checkHelperOwners(t, base, "during a bulk load")
	base.EndBulkLoad()
	checkHelperOwners(t, base, "after a bulk load")
	previous := base.ResetMaps()
	checkHelperOwners(t, base, "ResetMaps")
	base.BeginBulkLoad(16)
	for a, d := range previous {
		base.SetDescriptor(a, d)
	}
	base.EndBulkLoad()
	checkHelperOwners(t, base, "ResetMaps refill")
	if _, ok := base.Procedure(added.Address()); !ok {
		t.Fatal("refill lost an owner")
	}
}

func TestCatalogHelperOwnersFollowCandidates(t *testing.T) {
	base, ns, _ := catalogReadFixture()
	kept := helperTestOwner(3001, ns.Address())
	replaced := helperTestOwner(3011, ns.Address())
	removed := helperTestOwner(3021, ns.Address())
	for _, p := range []*Procedure{kept, replaced, removed} {
		base.SetDescriptor(p.Address(), p)
		base.BindName(DescriptorNameKey(p), p.Address())
	}
	c := NewCandidate(NewRead(nil, base))
	created := helperTestOwner(3031, ns.Address())
	if err := c.Create(created); err != nil {
		t.Fatal(err)
	}
	replacement := *replaced
	replacement.ObjectRevision++
	if err := c.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	c.RecordRemoval(ids.ObjectRef{Address: removed.Address()}, removed)
	next := c.State()
	checkHelperOwners(t, next, "Candidate.State")
	checkHelperOwners(t, base, "candidate base")
	for _, p := range NewRead(nil, next).HelperOwners() {
		if p == replaced || p == removed {
			t.Fatalf("Candidate.State kept %v", p.Address())
		}
	}
	applied := c.ApplyTo(CloneState(base))
	checkHelperOwners(t, applied, "ApplyTo")
}
