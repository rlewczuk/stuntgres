package catalog

import (
	"maps"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAdvanceRevisionFrontier(t *testing.T) {
	const max = ids.DefinitionRevision(^uint64(0))
	cases := []struct {
		name                             string
		candidate, revision, stamp, want ids.DefinitionRevision
		terminal                         bool
	}{
		{"already_past", 10, 9, 0, 10, false},
		{"equal", 10, 10, 0, 11, false},
		{"behind", 10, 12, 0, 13, false},
		{"sequence_stamp", 10, 4, 12, 13, false},
		{"ahead_of_stamp", 20, 4, 12, 20, false},
		{"equal_clocks", 0, 12, 12, 13, false},
		{"revision_penultimate", 0, max - 1, 0, max, false},
		{"stamp_penultimate", 0, 4, max - 1, max, false},
		{"revision_terminal", 0, max, 0, max, true},
		{"stamp_terminal", 0, 4, max, max, true},
		{"candidate_terminal_only", max, 4, 12, max, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Relation{ObjectRevision: tc.revision, SequencePlanStamp: tc.stamp}
			got, terminal := AdvanceRevisionFrontier(tc.candidate, d)
			if got != tc.want || terminal != tc.terminal {
				t.Fatalf("frontier = (%d, %v), want (%d, %v)", got, terminal, tc.want, tc.terminal)
			}
			if d.ObjectRevision != tc.revision || d.SequencePlanStamp != tc.stamp {
				t.Fatal("pure frontier calculation mutated the descriptor")
			}
		})
	}
	got, terminal := AdvanceRevisionFrontier(10, &Namespace{ObjectRevision: 12})
	if got != 13 || terminal {
		t.Fatalf("namespace frontier = (%d, %v)", got, terminal)
	}
}

func TestRevisionFrontierSkipsNilDescriptors(t *testing.T) {
	ds := map[ids.ObjectAddress]Descriptor{
		{ClassID: ids.RelationClassID, ObjectID: 1}:  &Relation{ObjectRevision: 7, SequencePlanStamp: 9},
		{ClassID: ids.RelationClassID, ObjectID: 2}:  nil,
		{ClassID: ids.NamespaceClassID, ObjectID: 3}: &Namespace{ObjectRevision: 4},
	}
	if got := RevisionFrontier(5, maps.All(ds)); got != 10 {
		t.Fatalf("frontier = %d, want 10", got)
	}
	if got := RevisionFrontier(12, maps.All(ds)); got != 12 {
		t.Fatalf("frontier past every clock = %d, want 12", got)
	}
	terminal := map[ids.ObjectAddress]Descriptor{{ClassID: ids.RelationClassID, ObjectID: 1}: &Relation{ObjectRevision: ids.DefinitionRevision(^uint64(0))}}
	if got := RevisionFrontier(5, maps.All(terminal)); got != ids.DefinitionRevision(^uint64(0)) {
		t.Fatalf("terminal frontier = %d", got)
	}
	if got := RevisionFrontier(5, maps.All(map[ids.ObjectAddress]Descriptor(nil))); got != 5 {
		t.Fatalf("empty frontier = %d, want 5", got)
	}
}
