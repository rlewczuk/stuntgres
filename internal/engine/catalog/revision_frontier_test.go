package catalog

import (
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
