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
	case *distinctNode, *aggregateNode, *setNode, *recursiveUnionNode, *worktableScanNode:
		return false // Materialized tuples no longer carry physical absence.
	case *derivedNode:
		if n.CTE != nil {
			return false
		}
		return absentLockedQuery(n.Query.Root, inputs, locks)
	case *sourceNode:
		return absentLockedSource(querySourceAt(inputs, n.Occurrence), locks)
	case *joinNode:
		return absentLockedQuery(n.Left, inputs, locks) && absentLockedQuery(n.Right, inputs, locks)
	case *projectNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *filterNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *evaluationNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *sortNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *distinctOnNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *limitNode:
		return absentLockedQuery(n.Child, inputs, locks)
	case *rowLockNode:
		return absentLockedQuery(n.Child, inputs, locks)
	}
	return false
}
