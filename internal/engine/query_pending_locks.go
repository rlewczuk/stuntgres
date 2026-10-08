package engine

import "context"

// Each manifest entry identifies one captured query owner and its expanded
// physical occurrences. Entries are immutable; frontier arbitration combines
// them in execution-private scratch, never in the shared capture leaf modes.
type queryPendingMark struct {
	owner       *rowLockNode
	inputs      *querySource
	occurrences []int
	mode        relationLockMode
}

func pendingQueryMarks(node LogicalNode, inputs *querySource, dst []queryPendingMark) []queryPendingMark {
	switch n := node.(type) {
	case *projectNode:
		return pendingQueryMarks(n.child, inputs, dst)
	case *filterNode:
		return pendingQueryMarks(n.child, inputs, dst)
	case *evaluationNode:
		return pendingQueryMarks(n.child, inputs, dst)
	case *rowLockNode:
		dst = pendingQueryMarks(n.child, inputs, dst)
		return append(dst, queryPendingMark{owner: n, inputs: inputs, occurrences: queryTreeOccurrences(n.child, inputs, nil), mode: n.mode})
	case *sourceNode:
		return pendingSourceMarks(querySourceAt(inputs, n.occurrence), dst)
	case *joinNode:
		dst = pendingQueryMarks(n.left, inputs, dst)
		return pendingQueryMarks(n.right, inputs, dst)
	case *sortNode, *aggregateNode, *limitNode:
		// These owners consume descendant pending marks. Own marks sit above
		// their Sort and therefore are discovered separately by rowLockNode.
		return dst
	default:
		return dst
	}
}
func pendingSourceMarks(source *querySource, dst []queryPendingMark) []queryPendingMark {
	if source.input != nil {
		return pendingSourceMarks(source.input, dst)
	}
	if source.nestedRoot != nil {
		return pendingQueryMarks(source.nestedRoot, source.nestedInputs, dst)
	}
	return dst
}
func settlePendingQueryMarks(ctx context.Context, state *queryState, node LogicalNode, child indexedStage) (indexedStage, error) {
	if len(child.pending) == 0 {
		return child, nil
	}
	return arbitrateViewQuery(ctx, state, node, child)
}
