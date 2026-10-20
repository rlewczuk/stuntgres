package engine

import (
	"fmt"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) RowAbsent(n plan.RowExpr) (bool, error) {
	if n.PresenceSlot == 0 && n.WholeNames == nil {
		return false, nil
	}
	for c != nil && (c.Scope != 1 || c.Frame != n.PresenceFrame) {
		c = c.Outer
	}
	if c == nil {
		return false, fmt.Errorf("stuntgres: missing whole-row source frame")
	}
	if n.MergeSource && !c.mergeSourcePresent {
		return true, nil
	}
	if n.PresenceSlot == 0 {
		return false, nil
	}
	if n.PresenceSlot > len(c.presenceSlots) {
		return false, fmt.Errorf("stuntgres: missing whole-row presence frame")
	}
	index := c.presenceSlots[n.PresenceSlot-1] - 1
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
func establishDeferredPresence(source *querySource, root plan.Node) {
	width := markPresenceTopology(root, nil)
	if width == 0 {
		return
	}
	source.presenceSlots = make([]int, width)
	markPresenceTopology(root, source.presenceSlots)
	source.presenceCount = packPresenceSlots(source.presenceSlots)
}

func markPresenceTopology(root plan.Node, slots []int) int {
	slot := 0
	switch n := root.(type) {
	case *plan.SourceNode:
		slot = n.PresenceSlot
	case *plan.ValuesNode:
		slot = n.PresenceSlot
	case *plan.DerivedNode:
		slot = n.PresenceSlot
	case *plan.WorktableScanNode:
		slot = n.PresenceSlot
	case *plan.FunctionFromNode:
		slot = n.PresenceSlot
	case *plan.JoinNode:
		slot = n.PresenceSlot
		width := max(markPresenceTopology(n.Left, slots), markPresenceTopology(n.Right, slots))
		if slots != nil && slot != 0 {
			slots[slot-1] = 1
		}
		return max(width, slot)
	case *plan.ProjectNode:
		return markPresenceTopology(n.Child, slots)
	case *plan.SortNode:
		return markPresenceTopology(n.Child, slots)
	case *plan.LimitNode:
		return markPresenceTopology(n.Child, slots)
	case *plan.FilterNode:
		return markPresenceTopology(n.Child, slots)
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

func (c *EvalContext) EvalSubquery(e plan.SubqueryExpr) (value sqltype.Value, err error) {
	if err = evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.query == nil || c.query.source == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing subquery statement capture")
	}
	if e.UpdateWidth != 0 {
		return c.subqueryUpdateOutputs(e)
	}
	if len(e.Membership) != 0 {
		return c.subqueryHashMembership(e)
	}
	var smallLeft [4]sqltype.Value
	left := smallLeft[:min(len(e.Comparisons), len(smallLeft))]
	if len(e.Comparisons) > len(smallLeft) {
		left = make([]sqltype.Value, len(e.Comparisons))
	}
	for i, comparison := range e.Comparisons {
		left[i], err = comparison.Left.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	state := queryState{source: c.query.source, parameters: c.Parameters, input: c.Input, output: c.Output, locking: c.query.locking, outer: c, ctes: c.query.ctes, hashOwner: c.query.membershipOwner()}
	stage, err := openQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
	if err != nil {
		return sqltype.Value{}, err
	}
	defer func() {
		closeErr := stage.Close()
		if err == nil {
			err = closeErr
		}
	}()
	if e.JSONArray != nil {
		return c.subqueryJSONArray(e, &state, &stage)
	}
	if e.Kind == parser.SubqueryExists && e.PresenceOnly {
		_, present, failure := stage.nextOccurrence(c.Context)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		return sqltype.Boolean(e.Usage.Type, present), nil
	}
	if e.Kind == parser.SubqueryExists {
		present, failure := stage.nextProjected(c.Context, nil)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		return sqltype.Boolean(e.Usage.Type, present), nil
	}
	width := len(stage.Schema())
	var small [4]sqltype.Value
	row := small[:min(width, len(small))]
	if width > len(small) {
		row = make([]sqltype.Value, width)
	}
	if e.Kind == parser.SubqueryScalar {
		present, failure := stage.Next(c.Context, row)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if !present {
			return sqltype.ResultNull(e.Usage), nil
		}
		value = row[0]
		extra, failure := stage.nextProjected(c.Context, nil)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if extra {
			return sqltype.Value{}, sqlError("21000", "more than one row returned by a subquery used as an expression")
		}
		return value, nil
	}
	unknown := false
	for {
		present, failure := stage.Next(c.Context, row)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if !present {
			if e.Kind == parser.SubqueryRow || unknown {
				return sqltype.Null(e.Usage.Type), nil
			}
			truth := e.Kind == parser.SubqueryAll
			if e.Negate {
				truth = !truth
			}
			return sqltype.Boolean(e.Usage.Type, truth), nil
		}
		rightContext := state.evaluation(c.Context)
		rightContext.Row = row
		answer, failure := e.Compare(&rightContext, left)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if e.Kind == parser.SubqueryRow {
			extra, failure := stage.nextProjected(c.Context, nil)
			if failure != nil {
				return sqltype.Value{}, failure
			}
			if extra {
				return sqltype.Value{}, sqlError("21000", "more than one row returned by a subquery used as an expression")
			}
			return answer, nil
		}
		if answer.IsNull() {
			unknown = true
			continue
		}
		truth, failure := answer.Boolean()
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if e.Kind == parser.SubqueryAll && !truth || e.Kind == parser.SubqueryAny && truth {
			if e.Negate {
				truth = !truth
			}
			return sqltype.Boolean(e.Usage.Type, truth), nil
		}
	}
}

// A captured source distinguishes aliases of the same frozen plan. Both remain
// stable across unrelated parent rescans, within one actual execution owner.
type subqueryMembershipKey struct {
	plan   *plan.Query
	source *querySource
}

type subqueryMembership struct {
	flat     []sqltype.Value
	nullRows []int
	buckets  map[uint64][]int
	scratch  []sqltype.Value
	full     bool
}

func (c *EvalContext) subqueryMembership(e plan.SubqueryExpr) (membership *subqueryMembership, err error) {
	state := queryState{source: c.query.source, parameters: c.Parameters, input: c.Input, output: c.Output, locking: c.query.locking, outer: c, ctes: c.query.ctes, hashOwner: c.query.membershipOwner()}
	stage, err := openQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := stage.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = c.Context.Err()
		}
		if err != nil {
			membership = nil
		}
	}()
	width := len(e.Membership)
	membership = &subqueryMembership{}
	var small [4]sqltype.Value
	row := small[:min(width, len(small))]
	if width > len(small) {
		row = make([]sqltype.Value, width)
	}
	for {
		present, failure := stage.Next(c.Context, row)
		if failure != nil {
			return nil, failure
		}
		if !present {
			return membership, c.Context.Err()
		}
		if membership.scratch == nil {
			membership.scratch = make([]sqltype.Value, width)
			membership.buckets = make(map[uint64][]int)
		}
		rightContext := state.evaluation(c.Context)
		rightContext.Row = row
		hash := uint64(14695981039346656037)
		hasNull := false
		for i, comparison := range e.Comparisons {
			value, failure := comparison.Right.Eval(&rightContext)
			if failure != nil {
				return nil, failure
			}
			value, failure = e.Membership[i].PrepareKey(c.Context, value)
			if failure != nil {
				return nil, failure
			}
			membership.scratch[i] = value
			hasNull = hasNull || value.IsNull()
			h, failure := e.Membership[i].Hash(c.Context, value)
			if failure != nil {
				return nil, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		count := len(membership.flat) / width
		if !hasNull {
			membership.full = true
			index, failure := findDistinctTuple(c.Context, e.Membership, membership.scratch, membership.flat, membership.buckets[hash])
			if failure != nil {
				return nil, failure
			}
			if index >= 0 {
				continue
			}
			membership.buckets[hash] = append(membership.buckets[hash], count)
		} else {
			membership.nullRows = append(membership.nullRows, count)
		}
		membership.flat = append(membership.flat, membership.scratch...)
	}
}

func (c *EvalContext) subqueryHashMembership(e plan.SubqueryExpr) (sqltype.Value, error) {
	owner := c.query.membershipOwner()
	key := subqueryMembershipKey{e.QueryPlan, c.query.source}
	membership := owner.memberships[key]
	if membership == nil {
		var err error
		membership, err = c.subqueryMembership(e)
		if err != nil {
			return sqltype.Value{}, err
		}
		if owner.memberships == nil {
			owner.memberships = make(map[subqueryMembershipKey]*subqueryMembership)
		}
		owner.memberships[key] = membership
	}
	// Empty RHS truth is known before any LHS projection, provider or effect.
	if len(membership.flat) == 0 {
		return sqltype.Boolean(e.Usage.Type, e.Negate), nil
	}
	left := membership.scratch
	leftNull := false
	for i, comparison := range e.Comparisons {
		value, err := comparison.Left.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		left[i], err = e.Membership[i].PrepareKey(c.Context, value)
		if err != nil {
			return sqltype.Value{}, err
		}
		leftNull = leftNull || left[i].IsNull()
	}
	if membership.full && !leftNull {
		hash := uint64(14695981039346656037)
		for i, value := range left {
			h, err := e.Membership[i].Hash(c.Context, value)
			if err != nil {
				return sqltype.Value{}, err
			}
			hash = (hash ^ h) * 1099511628211
		}
		index, err := findDistinctTuple(c.Context, e.Membership, left, membership.flat, membership.buckets[hash])
		if err != nil {
			return sqltype.Value{}, err
		}
		if index >= 0 {
			return sqltype.Boolean(e.Usage.Type, !e.Negate), nil
		}
	}
	// Keep NULL row indexes immutable: every probe needs the same partial-match
	// evidence. A decisive unequal nonNULL field can still establish FALSE.
	width := len(e.Membership)
	candidates := len(membership.nullRows)
	if leftNull {
		candidates = len(membership.flat) / width
	}
	for candidate := 0; candidate < candidates; candidate++ {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		index := candidate
		if !leftNull {
			index = membership.nullRows[candidate]
		}
		right := membership.flat[index*width : (index+1)*width]
		unknown, unequal := false, false
		for i, comparison := range e.Comparisons {
			value, err := comparison.Predicate.Evaluate(c.Context, left[i], right[i], c.Input)
			if err != nil {
				return sqltype.Value{}, err
			}
			if value.IsNull() {
				unknown = true
				continue
			}
			truth, err := value.Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
			if !truth {
				unequal = true
				break
			}
		}
		if unknown && !unequal {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	return sqltype.Boolean(e.Usage.Type, e.Negate), nil
}
