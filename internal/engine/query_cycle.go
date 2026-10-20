package engine

// Hidden parent projections use the actual joined worktable occurrence, never
// generation order. The legality walker guarantees exactly one top-level self.
func recursiveParentSlot(node LogicalNode, d *cteDefinition, offset int) (*worktableScanNode, int) {
	switch n := node.(type) {
	case *worktableScanNode:
		if n.CTE == d {
			return n, offset
		}
	case *joinNode:
		if scan, slot := recursiveParentSlot(n.Left, d, offset); scan != nil {
			return scan, slot
		}
		return recursiveParentSlot(n.Right, d, offset+n.LeftWidth)
	default:
		left, _ := queryInputs(node)
		if left != nil {
			return recursiveParentSlot(left, d, offset)
		}
	}
	return nil, 0
}
