package index

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestCandidatesMatch(t *testing.T) {
	relation := func(oid ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	}
	parent := &catalog.Relation{ObjectAddress: relation(10), ObjectName: "t"}
	committed := &catalog.Relation{ObjectAddress: relation(11), ObjectName: "committed_idx", SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Ready: true}}
	staged := &catalog.Relation{ObjectAddress: relation(12), ObjectName: "staged_idx", SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Ready: true}}
	state := catalog.NewState(1)
	for _, d := range []catalog.Descriptor{parent, committed} {
		state.SetDescriptor(d.Address(), d)
	}
	candidate := catalog.NewCandidate(catalog.NewRead(nil, state))
	candidate.UpdateDescriptor(staged)

	store := &storage.Relation{Versions: make([]storage.RowVersion, 1), Generation: 2}
	other := &storage.Relation{Versions: make([]storage.RowVersion, 1), Generation: 2}
	root := func(index *catalog.Relation) RootCandidate {
		return RootCandidate{Index: index, Storage: store, Generation: 2, Length: 1}
	}
	var parents []ids.ObjectAddress
	storeOf := func(want *storage.Relation) func(ids.ObjectAddress) *storage.Relation {
		return func(a ids.ObjectAddress) *storage.Relation {
			parents = append(parents, a)
			return want
		}
	}
	both := map[ids.ObjectAddress]RootCandidate{committed.Address(): root(committed), staged.Address(): root(staged)}
	if !CandidatesMatch(both, &candidate, state, storeOf(store)) {
		t.Fatal("committed and staged candidates did not match")
	}
	if len(parents) != 2 || parents[0] != parent.Address() || parents[1] != parent.Address() {
		t.Fatalf("storeOf parents = %v", parents)
	}
	if !CandidatesMatch(nil, &candidate, state, storeOf(nil)) {
		t.Fatal("no candidates did not match")
	}
	cases := map[string]map[ids.ObjectAddress]RootCandidate{
		"missing index":      {relation(13): root(committed)},
		"non-index relation": {parent.Address(): root(parent)},
	}
	for name, candidates := range cases {
		if CandidatesMatch(candidates, &candidate, state, storeOf(store)) {
			t.Fatalf("%s matched", name)
		}
	}
	if CandidatesMatch(both, &candidate, state, storeOf(other)) {
		t.Fatal("candidate matched another store")
	}
	// A staged replacement shadows the committed index.
	replaced := *committed
	replaced.SQLIndex = &catalog.IndexDefinition{Parent: parent.Address(), Ready: true, Unique: true}
	candidate.UpdateDescriptor(&replaced)
	if CandidatesMatch(map[ids.ObjectAddress]RootCandidate{committed.Address(): root(committed)}, &candidate, state, storeOf(store)) {
		t.Fatal("candidate matched the committed index under a staged replacement")
	}
}
