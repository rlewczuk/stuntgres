package engine

import "stuntgres/internal/engine/plan"

// Hidden parent projections use the actual joined worktable occurrence, never
// generation order. The legality walker guarantees exactly one top-level self.
func recursiveParentSlot(node plan.Node, d *plan.CTEDefinition, offset int) (*plan.WorktableScanNode, int) {
	switch n := node.(type) {
	case *plan.WorktableScanNode:
		if n.CTE == d {
			return n, offset
		}
	case *plan.JoinNode:
		if scan, slot := recursiveParentSlot(n.Left, d, offset); scan != nil {
			return scan, slot
		}
		return recursiveParentSlot(n.Right, d, offset+n.LeftWidth)
	default:
		left, _ := plan.QueryInputs(node)
		if left != nil {
			return recursiveParentSlot(left, d, offset)
		}
	}
	return nil, 0
}
