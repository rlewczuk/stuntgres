package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

// seededEntries returns a freshly seeded state and the descriptors its seed
// installed.
func seededEntries(t *testing.T) (*State, map[ids.ObjectAddress]Descriptor) {
	t.Helper()
	s := seedFixture(&SeedShare{}, "unseeded", "").State
	seeded := map[ids.ObjectAddress]Descriptor{}
	for a, d := range s.Descriptors() {
		seeded[a] = d
	}
	if len(seeded) < 3 {
		t.Fatalf("seed installed %d descriptors", len(seeded))
	}
	return s, seeded
}

// checkUnseeded requires UnseededDescriptors to visit, once and with its
// current value, exactly each entry that is not the descriptor the seed
// installed at its address (every entry when all is set), and Unseeded to
// agree.
func checkUnseeded(t *testing.T, s *State, seeded map[ids.ObjectAddress]Descriptor, all bool, label string) {
	t.Helper()
	view := NewRead(nil, s)
	want := map[ids.ObjectAddress]Descriptor{}
	for a, d := range s.Descriptors() {
		if all || seeded[a] != d {
			want[a] = d
		}
		if view.Unseeded(a) != (all || seeded[a] != d) {
			t.Fatalf("%s: Unseeded(%v) = %v", label, a, view.Unseeded(a))
		}
	}
	got := map[ids.ObjectAddress]Descriptor{}
	for a, d := range view.UnseededDescriptors() {
		if _, dup := got[a]; dup {
			t.Fatalf("%s: UnseededDescriptors visited %v twice", label, a)
		}
		got[a] = d
	}
	if len(got) != len(want) {
		t.Fatalf("%s: UnseededDescriptors has %d entries, want %d", label, len(got), len(want))
	}
	for a, d := range want {
		if got[a] != d {
			t.Fatalf("%s: UnseededDescriptors[%v] = %p, want %p", label, a, got[a], d)
		}
	}
}

func TestCatalogUnseededFollowStateWrites(t *testing.T) {
	s, seeded := seededEntries(t)
	checkUnseeded(t, s, seeded, false, "seed")
	var ns *Namespace
	ceiling := ids.OID(0)
	for a, d := range seeded {
		ceiling = max(ceiling, a.ObjectID)
		if n, ok := d.(*Namespace); ok && ns == nil {
			ns = n
		}
	}
	view := NewRead(nil, s)
	if !view.SeedMayReference(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ceiling}) || view.SeedMayReference(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ceiling + 1}) {
		t.Fatalf("SeedMayReference does not stop at the seed's highest OID %d", ceiling)
	}
	added := catalogTestRelation(ceiling+10, ns.Address())
	s.SetDescriptor(added.Address(), added)
	checkUnseeded(t, s, seeded, false, "added")
	renamed := *ns
	renamed.ObjectRevision++
	s.SetDescriptor(ns.Address(), &renamed)
	checkUnseeded(t, s, seeded, false, "seeded entry replaced")
	published := CloneState(s)
	s.RemoveDescriptor(added.Address())
	s.RemoveDescriptor(ns.Address())
	checkUnseeded(t, s, seeded, false, "removed")
	checkUnseeded(t, published, seeded, false, "copy before removal")
	s.ResetDescriptors()
	s.SetDescriptor(ns.Address(), ns)
	checkUnseeded(t, s, nil, false, "written after ResetDescriptors")
}

func TestCatalogUnseededFollowCandidates(t *testing.T) {
	base, seeded := seededEntries(t)
	var ns *Namespace
	var removed Descriptor
	for _, d := range seeded {
		if n, ok := d.(*Namespace); ok {
			if ns == nil {
				ns = n
			} else if removed == nil {
				removed = n
			}
		}
	}
	c := NewCandidate(NewRead(nil, base))
	created := catalogTestRelation(70001, ns.Address())
	if err := c.Create(created); err != nil {
		t.Fatal(err)
	}
	replacement := *ns
	replacement.ObjectRevision++
	if err := c.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	c.RecordRemoval(ids.ObjectRef{Address: removed.Address()}, removed)
	next := c.State()
	checkUnseeded(t, next, seeded, false, "Candidate.State")
	checkUnseeded(t, base, seeded, false, "candidate base")
	if NewRead(nil, next).SeedMayReference(created.Address()) {
		t.Fatal("Candidate.State lost the seed ceiling")
	}
}

// Any bulk load but the seed's rewrites the whole catalog, so it marks every
// entry unseeded, and so does a seed over a state with unseeded entries.
func TestCatalogUnseededBulkLoadsMarkAll(t *testing.T) {
	s, seeded := seededEntries(t)
	s.BeginBulkLoad(16)
	checkUnseeded(t, s, seeded, true, "during a bulk load")
	s.EndBulkLoad()
	checkUnseeded(t, s, seeded, true, "after a bulk load")
	clone := CloneState(s)
	checkUnseeded(t, clone, seeded, true, "copy after a bulk load")
	var ns *Namespace
	for _, d := range seeded {
		if n, ok := d.(*Namespace); ok {
			ns = n
			break
		}
	}
	candidate := NewCandidate(NewRead(nil, clone))
	next := candidate.State()
	checkUnseeded(t, next, seeded, true, "candidate after a bulk load")
	s.ResetDescriptors()
	s.SetDescriptor(ns.Address(), ns)
	checkUnseeded(t, s, nil, false, "ResetDescriptors clears the mark")

	store := newSeedStore(&SeedShare{})
	store.State.EndBulkLoad()
	store.State.SetDescriptor(ns.Address(), ns)
	store.State.BeginBulkLoad(16)
	store.State.bulk.seed = &seedBinds{}
	store.State.EndBulkLoad()
	checkUnseeded(t, store.State, nil, true, "seed over an unseeded entry")
}
