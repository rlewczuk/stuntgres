package analyze

import (
	"fmt"
	"stuntgres/internal/engine/plan"
)

// Slots belong to one bound query frame. Only namespace occurrences which may
// be null-extended get a slot; ordinary queries and proven-present rows do not.
func (b *Binder) WholeRowPresence(q *QueryQualifier, frame uint32) (int, error) {
	if !q.NullExtended {
		return 0, nil
	}
	var slot *int
	root := q.SourceRoot
unwrap:
	for {
		switch n := root.(type) {
		case *plan.ProjectNode:
			root = n.Child
		case *plan.SortNode:
			root = n.Child
		case *plan.LimitNode:
			root = n.Child
		case *plan.FilterNode:
			root = n.Child
		default:
			break unwrap
		}
	}
	switch n := root.(type) {
	case *plan.SourceNode:
		slot = &n.PresenceSlot
	case *plan.ValuesNode:
		slot = &n.PresenceSlot
	case *plan.DerivedNode:
		slot = &n.PresenceSlot
	case *plan.WorktableScanNode:
		slot = &n.PresenceSlot
	case *plan.JoinNode:
		slot = &n.PresenceSlot
	case *plan.FunctionFromNode:
		slot = &n.PresenceSlot
	default:
		return 0, fmt.Errorf("stuntgres: missing whole-row source occurrence")
	}
	if *slot == 0 {
		if b.Query.presenceFrames == nil {
			b.Query.presenceFrames = make(map[uint32]int)
		}
		b.Query.presenceFrames[frame]++
		*slot = b.Query.presenceFrames[frame]
	}
	return *slot, nil
}
