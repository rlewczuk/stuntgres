package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// AdvanceRevisionFrontier calculates a speculative allocator frontier without
// reserving it. Publication commits the scalar under Engine.mu only at install.
// terminal describes the descriptor clocks, not saturation of the result.
func AdvanceRevisionFrontier(candidate ids.DefinitionRevision, d Descriptor) (next ids.DefinitionRevision, terminal bool) {
	frontier := d.Revision()
	if relation, ok := d.(*Relation); ok && relation.SequencePlanStamp > frontier {
		frontier = relation.SequencePlanStamp
	}
	if frontier == ids.DefinitionRevision(^uint64(0)) {
		return frontier, true
	}
	if candidate > frontier {
		return candidate, false
	}
	return frontier + 1, false
}

// RevisionFrontier advances start over every non-nil descriptor of ds with
// AdvanceRevisionFrontier. It is pure and reserves nothing.
func RevisionFrontier(start ids.DefinitionRevision, ds iter.Seq2[ids.ObjectAddress, Descriptor]) ids.DefinitionRevision {
	for _, d := range ds {
		if d != nil {
			start, _ = AdvanceRevisionFrontier(start, d)
		}
	}
	return start
}
