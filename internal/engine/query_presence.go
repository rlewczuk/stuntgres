package engine

import "fmt"

// Slots belong to one bound query frame. Only namespace occurrences which may
// be null-extended get a slot; ordinary queries and proven-present rows do not.
func (b *expressionBinder) wholeRowPresence(q *queryQualifier, frame uint32) (int, error) {
	if !q.nullExtended {
		return 0, nil
	}
	var slot *int
	root := q.sourceRoot
unwrap:
	for {
		switch n := root.(type) {
		case *projectNode:
			root = n.child
		case *sortNode:
			root = n.child
		case *limitNode:
			root = n.child
		case *filterNode:
			root = n.child
		default:
			break unwrap
		}
	}
	switch n := root.(type) {
	case *sourceNode:
		slot = &n.presenceSlot
	case *valuesNode:
		slot = &n.presenceSlot
	case *derivedNode:
		slot = &n.presenceSlot
	case *joinNode:
		slot = &n.presenceSlot
	case *functionFromNode:
		slot = &n.presenceSlot
	default:
		return 0, fmt.Errorf("stuntgres: missing whole-row source occurrence")
	}
	if *slot == 0 {
		if b.presenceFrames == nil {
			b.presenceFrames = make(map[uint32]int)
		}
		b.presenceFrames[frame]++
		*slot = b.presenceFrames[frame]
	}
	return *slot, nil
}

func (n rowExpression) absent(c *EvalContext) (bool, error) {
	if n.presenceSlot == 0 && n.wholeNames == nil {
		return false, nil
	}
	for c != nil && (c.Scope != 1 || c.Frame != n.presenceFrame) {
		c = c.Outer
	}
	if c == nil {
		return false, fmt.Errorf("stuntgres: missing whole-row source frame")
	}
	if n.presenceSlot == 0 {
		return false, nil
	}
	if n.presenceSlot > len(c.presenceSlots) {
		return false, fmt.Errorf("stuntgres: missing whole-row presence frame")
	}
	index := c.presenceSlots[n.presenceSlot-1] - 1
	if index < 0 || index >= len(c.presence) {
		return false, fmt.Errorf("stuntgres: missing whole-row presence occurrence")
	}
	return !c.presence[index], nil
}

func establishSourcePresence(source *querySource, slot int) {
	if slot == 0 {
		return
	}
	source.presenceSlots = make([]int, slot)
	source.presenceSlots[slot-1] = 1
	source.presenceConstant = []bool{true}
	source.presenceCount = 1
}

func (source *querySource) presenceRow(index int) []bool {
	if index == absentQueryIndex || source.presenceCount == 0 {
		return nil
	}
	if source.window {
		if source.windowIndices != nil {
			index = source.windowIndices[index]
		}
		return source.input.presenceRow(index)
	}
	if len(source.presenceConstant) != 0 {
		return source.presenceConstant
	}
	start := index * source.presenceCount
	return source.presenceFlat[start : start+source.presenceCount]
}

// Child slots map once into a join's packed flag vector. There are no maps or
// occurrence objects per tuple, and no flags at all without a genuine demand.
type presenceCopy struct {
	left, right int
	own         bool
}

// Deferred correlated sources must expose the same demanded layout as their
// eventual invocation before a parent borrows it. Discover only bound slots:
// no child stage is opened and no argument is evaluated.
func establishDeferredPresence(source *querySource, root LogicalNode) {
	width := markPresenceTopology(root, nil)
	if width == 0 {
		return
	}
	source.presenceSlots = make([]int, width)
	markPresenceTopology(root, source.presenceSlots)
	source.presenceCount = packPresenceSlots(source.presenceSlots)
}

func markPresenceTopology(root LogicalNode, slots []int) int {
	slot := 0
	switch n := root.(type) {
	case *sourceNode:
		slot = n.presenceSlot
	case *valuesNode:
		slot = n.presenceSlot
	case *derivedNode:
		slot = n.presenceSlot
	case *functionFromNode:
		slot = n.presenceSlot
	case *joinNode:
		slot = n.presenceSlot
		width := max(markPresenceTopology(n.left, slots), markPresenceTopology(n.right, slots))
		if slots != nil && slot != 0 {
			slots[slot-1] = 1
		}
		return max(width, slot)
	case *projectNode:
		return markPresenceTopology(n.child, slots)
	case *sortNode:
		return markPresenceTopology(n.child, slots)
	case *limitNode:
		return markPresenceTopology(n.child, slots)
	case *filterNode:
		return markPresenceTopology(n.child, slots)
	}
	if slots != nil && slot != 0 {
		slots[slot-1] = 1
	}
	return slot
}

// Slot order is the single packed-index authority for both deferred topology
// and actual sources. Layouts never change after an evaluation frame borrows them.
func packPresenceSlots(slots []int) int {
	count := 0
	for i, slot := range slots {
		if slot != 0 {
			count++
			slots[i] = count
		}
	}
	return count
}

func joinPresence(source, left, right *querySource, own int) ([]presenceCopy, []bool) {
	width := max(len(left.presenceSlots), len(right.presenceSlots), own)
	if width == 0 {
		return nil, nil
	}
	source.presenceSlots = make([]int, width)
	for i := range width {
		l, r := 0, 0
		if i < len(left.presenceSlots) {
			l = left.presenceSlots[i]
		}
		if i < len(right.presenceSlots) {
			r = right.presenceSlots[i]
		}
		if l != 0 || r != 0 || own == i+1 {
			source.presenceSlots[i] = 1
		}
	}
	source.presenceCount = packPresenceSlots(source.presenceSlots)
	moves := make([]presenceCopy, source.presenceCount)
	for i, slot := range source.presenceSlots {
		if slot == 0 {
			continue
		}
		move := presenceCopy{left: -1, right: -1, own: own == i+1}
		if i < len(left.presenceSlots) {
			move.left = left.presenceSlots[i] - 1
		}
		if i < len(right.presenceSlots) {
			move.right = right.presenceSlots[i] - 1
		}
		moves[slot-1] = move
	}
	return moves, make([]bool, source.presenceCount)
}
func fillJoinPresence(flags []bool, moves []presenceCopy, left, right []bool) {
	for i, move := range moves {
		flags[i] = move.own || move.left >= 0 && move.left < len(left) && left[move.left] || move.right >= 0 && move.right < len(right) && right[move.right]
	}
}

func (c *EvalContext) setQueryPresence(source *querySource, index int) {
	c.presenceSlots = source.presenceSlots
	c.presence = source.presenceRow(index)
}
