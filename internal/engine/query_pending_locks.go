package engine

import (
	"context"
	"stuntgres/internal/parser"
)

// Each manifest entry carries a requested mode for expanded physical occurrence
// identities. Entries are immutable; frontier arbitration combines them in
// execution-private scratch, never in the shared capture leaf modes.
type queryPendingMark struct {
	occurrences []int
	mark        queryRowMark
}

func pendingQueryMarks(node LogicalNode, inputs *querySource, dst []queryPendingMark) []queryPendingMark {
	switch n := node.(type) {
	case *derivedNode:
		// The derived projection frontier consumes its own child marks.
		return dst
	case *projectNode:
		return pendingQueryMarks(n.child, inputs, dst)
	case *filterNode:
		return pendingQueryMarks(n.child, inputs, dst)
	case *evaluationNode:
		return pendingQueryMarks(n.child, inputs, dst)
	case *rowLockNode:
		dst = pendingQueryMarks(n.child, inputs, dst)
		return appendPendingRowMarks(dst, n.marks, inputs)
	case *sourceNode:
		return pendingSourceMarks(querySourceAt(inputs, n.occurrence), dst)
	case *joinNode:
		if n.kind != parser.JoinInner {
			// Outer joins consume each local input mark before ON matching.
			// Do not lift it back onto matched or NULL-extended output.
			return dst
		}
		dst = pendingQueryMarks(n.left, inputs, dst)
		return pendingQueryMarks(n.right, inputs, dst)
	case *sortNode, *aggregateNode, *distinctNode, *distinctOnNode, *setNode, *limitNode:
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
