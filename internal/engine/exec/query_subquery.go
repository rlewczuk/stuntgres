package exec

import (
	"fmt"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *Context) RowAbsent(n plan.RowExpr) (bool, error) {
	if n.PresenceSlot == 0 && n.WholeNames == nil {
		return false, nil
	}
	for c != nil && (c.Scope != 1 || c.Frame != n.PresenceFrame) {
		c = c.Outer
	}
	if c == nil {
		return false, fmt.Errorf("stuntgres: missing whole-row source frame")
	}
	if n.MergeSource && !c.MergeSourcePresent {
		return true, nil
	}
	if n.PresenceSlot == 0 {
		return false, nil
	}
	if n.PresenceSlot > len(c.PresenceSlots) {
		return false, fmt.Errorf("stuntgres: missing whole-row presence frame")
	}
	index := c.PresenceSlots[n.PresenceSlot-1] - 1
	if index < 0 || index >= len(c.Presence) {
		return false, fmt.Errorf("stuntgres: missing whole-row presence occurrence")
	}
	return !c.Presence[index], nil
}

func EstablishSourcePresence(source *Source, slot int) {
	if slot == 0 {
		return
	}
	source.PresenceSlots = make([]int, slot)
	source.PresenceSlots[slot-1] = 1
	source.PresenceConstant = []bool{true}
	source.PresenceCount = 1
}

func (source *Source) PresenceRow(index int) []bool {
	if index == AbsentQueryIndex || source.PresenceCount == 0 {
		return nil
	}
	if source.Window {
		if source.WindowIndices != nil {
			index = source.WindowIndices[index]
		}
		return source.Input.PresenceRow(index)
	}
	if len(source.PresenceConstant) != 0 {
		return source.PresenceConstant
	}
	start := index * source.PresenceCount
	return source.presenceFlat[start : start+source.PresenceCount]
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
func establishDeferredPresence(source *Source, root plan.Node) {
	width := markPresenceTopology(root, nil)
	if width == 0 {
		return
	}
	source.PresenceSlots = make([]int, width)
	markPresenceTopology(root, source.PresenceSlots)
	source.PresenceCount = packPresenceSlots(source.PresenceSlots)
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

func joinPresence(source, left, right *Source, own int) ([]presenceCopy, []bool) {
	width := max(len(left.PresenceSlots), len(right.PresenceSlots), own)
	if width == 0 {
		return nil, nil
	}
	source.PresenceSlots = make([]int, width)
	for i := range width {
		l, r := 0, 0
		if i < len(left.PresenceSlots) {
			l = left.PresenceSlots[i]
		}
		if i < len(right.PresenceSlots) {
			r = right.PresenceSlots[i]
		}
		if l != 0 || r != 0 || own == i+1 {
			source.PresenceSlots[i] = 1
		}
	}
	source.PresenceCount = packPresenceSlots(source.PresenceSlots)
	moves := make([]presenceCopy, source.PresenceCount)
	for i, slot := range source.PresenceSlots {
		if slot == 0 {
			continue
		}
		move := presenceCopy{left: -1, right: -1, own: own == i+1}
		if i < len(left.PresenceSlots) {
			move.left = left.PresenceSlots[i] - 1
		}
		if i < len(right.PresenceSlots) {
			move.right = right.PresenceSlots[i] - 1
		}
		moves[slot-1] = move
	}
	return moves, make([]bool, source.PresenceCount)
}

func fillJoinPresence(flags []bool, moves []presenceCopy, left, right []bool) {
	for i, move := range moves {
		flags[i] = move.own || move.left >= 0 && move.left < len(left) && left[move.left] || move.right >= 0 && move.right < len(right) && right[move.right]
	}
}

func (c *Context) setQueryPresence(source *Source, index int) {
	c.PresenceSlots = source.PresenceSlots
	c.Presence = source.PresenceRow(index)
}

func (c *Context) EvalSubquery(e plan.SubqueryExpr) (value sqltype.Value, err error) {
	if err = EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.Query == nil || c.Query.Source == nil {
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
	state := State{Source: c.Query.Source, Parameters: c.Parameters, Input: c.Input, Output: c.Output, Locking: c.Query.Locking, Outer: c, Ctes: c.Query.Ctes, hashOwner: c.Query.membershipOwner()}
	stage, err := OpenQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
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
		_, present, failure := stage.NextOccurrence(c.Context)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		return sqltype.Boolean(e.Usage.Type, present), nil
	}
	if e.Kind == parser.SubqueryExists {
		present, failure := stage.NextProjected(c.Context, nil)
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
		extra, failure := stage.NextProjected(c.Context, nil)
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
		rightContext := state.Evaluation(c.Context)
		rightContext.Row = row
		answer, failure := e.Compare(&rightContext, left)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if e.Kind == parser.SubqueryRow {
			extra, failure := stage.NextProjected(c.Context, nil)
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
type SubqueryMembershipKey struct {
	Plan   *plan.Query
	Source *Source
}

type subqueryMembership struct {
	Flat     []sqltype.Value
	nullRows []int
	Buckets  map[uint64][]int
	Scratch  []sqltype.Value
	full     bool
}

func (c *Context) subqueryMembership(e plan.SubqueryExpr) (membership *subqueryMembership, err error) {
	state := State{Source: c.Query.Source, Parameters: c.Parameters, Input: c.Input, Output: c.Output, Locking: c.Query.Locking, Outer: c, Ctes: c.Query.Ctes, hashOwner: c.Query.membershipOwner()}
	stage, err := OpenQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
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
		if membership.Scratch == nil {
			membership.Scratch = make([]sqltype.Value, width)
			membership.Buckets = make(map[uint64][]int)
		}
		rightContext := state.Evaluation(c.Context)
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
			membership.Scratch[i] = value
			hasNull = hasNull || value.IsNull()
			h, failure := e.Membership[i].Hash(c.Context, value)
			if failure != nil {
				return nil, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		count := len(membership.Flat) / width
		if !hasNull {
			membership.full = true
			index, failure := FindDistinctTuple(c.Context, e.Membership, membership.Scratch, membership.Flat, membership.Buckets[hash])
			if failure != nil {
				return nil, failure
			}
			if index >= 0 {
				continue
			}
			membership.Buckets[hash] = append(membership.Buckets[hash], count)
		} else {
			membership.nullRows = append(membership.nullRows, count)
		}
		membership.Flat = append(membership.Flat, membership.Scratch...)
	}
}

func (c *Context) subqueryHashMembership(e plan.SubqueryExpr) (sqltype.Value, error) {
	owner := c.Query.membershipOwner()
	key := SubqueryMembershipKey{e.QueryPlan, c.Query.Source}
	membership := owner.Memberships[key]
	if membership == nil {
		var err error
		membership, err = c.subqueryMembership(e)
		if err != nil {
			return sqltype.Value{}, err
		}
		if owner.Memberships == nil {
			owner.Memberships = make(map[SubqueryMembershipKey]*subqueryMembership)
		}
		owner.Memberships[key] = membership
	}
	// Empty RHS truth is known before any LHS projection, provider or effect.
	if len(membership.Flat) == 0 {
		return sqltype.Boolean(e.Usage.Type, e.Negate), nil
	}
	left := membership.Scratch
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
		index, err := FindDistinctTuple(c.Context, e.Membership, left, membership.Flat, membership.Buckets[hash])
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
		candidates = len(membership.Flat) / width
	}
	for candidate := 0; candidate < candidates; candidate++ {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		index := candidate
		if !leftNull {
			index = membership.nullRows[candidate]
		}
		right := membership.Flat[index*width : (index+1)*width]
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
