package catalog

import "stuntgres/internal/engine/ids"

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
