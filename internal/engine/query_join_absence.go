package engine

// Absence is carried by occurrence identity, never inferred from NULL cells or
// a zero-width physical row. Missing subtrees must not evaluate their recipes.
func absentLockedSource(source *querySource, locks *queryLockContext) bool {
	if source.input != nil {
		return absentLockedSource(source.input, locks)
	}
	if source.lineage != nil {
		return source.lineage.currentIndex == absentQueryIndex
	}
	if source.singleton || source.materialized {
		return false
	}
	return locks.leaves[source.occurrence].absent
}
func absentLockedQuery(node LogicalNode, inputs *querySource, locks *queryLockContext) bool {
	switch n := node.(type) {
	case *sourceNode:
		return absentLockedSource(querySourceAt(inputs, n.occurrence), locks)
	case *joinNode:
		return absentLockedQuery(n.left, inputs, locks) && absentLockedQuery(n.right, inputs, locks)
	case *projectNode:
		return absentLockedQuery(n.child, inputs, locks)
	case *filterNode:
		return absentLockedQuery(n.child, inputs, locks)
	case *evaluationNode:
		return absentLockedQuery(n.child, inputs, locks)
	case *sortNode:
		return absentLockedQuery(n.child, inputs, locks)
	case *limitNode:
		return absentLockedQuery(n.child, inputs, locks)
	case *rowLockNode:
		return absentLockedQuery(n.child, inputs, locks)
	}
	return false
}
