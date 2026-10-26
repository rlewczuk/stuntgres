package exec

import (
	"context"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type windowPartition struct {
	keys           []sqltype.Value
	rows, peerEnds []int
}
type WindowOrderState struct {
	values     []sqltype.Value
	partitions []windowPartition
}
type windowFrameOffsets struct {
	start, end               sqltype.Value
	integerStart, integerEnd int64
}

func CompareWindowRows(ctx context.Context, keys []plan.SortKey, values []sqltype.Value, a, b int, input sqltype.InputEnvironment) (int, error) {
	width := len(keys)
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		left, right := values[a*width+i], values[b*width+i]
		if left.IsNull() && right.IsNull() {
			continue
		}
		if left.IsNull() || right.IsNull() {
			if left.IsNull() == key.NullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		sign, err := key.Comparison.Compare(ctx, left, right, input)
		if err != nil {
			return 0, err
		}
		if sign != 0 {
			if key.Desc {
				sign = -sign
			}
			return sign, nil
		}
	}
	return 0, ctx.Err()
}

func evaluateWindowOffsets(ctx context.Context, state *State, spec plan.WindowSpec) (windowFrameOffsets, error) {
	var values windowFrameOffsets
	evaluation := state.Evaluation(ctx)
	for i, bound := range [2]plan.WindowBound{spec.Frame.Start, spec.Frame.End} {
		if bound.Offset == nil {
			continue
		}
		value, err := bound.Offset.Eval(&evaluation)
		if err != nil {
			return values, err
		}
		if err := ValidateProducedValue(value, bound.Offset.ResultUsage()); err != nil {
			return values, err
		}
		if value.IsNull() {
			word := "starting"
			if i == 1 {
				word = "ending"
			}
			return values, sqlError("22004", "frame "+word+" offset must not be null")
		}
		if spec.Frame.Unit == parser.FrameGroups || spec.Frame.Unit == parser.FrameRows {
			size, err := value.Integer()
			if err != nil {
				return values, err
			}
			if err := ctx.Err(); err != nil {
				return values, err
			}
			if size < 0 {
				word := "starting"
				if i == 1 {
					word = "ending"
				}
				return values, sqlError("22013", "frame "+word+" offset must not be negative")
			}
			if i == 0 {
				values.integerStart = size
			} else {
				values.integerEnd = size
			}
		}
		if i == 0 {
			values.start = value
		} else {
			values.end = value
		}
	}
	return values, ctx.Err()
}

func prepareWindowOrder(ctx context.Context, state *State, child *Stage, spec plan.WindowSpec) (WindowOrderState, error) {
	count := child.Count()
	partitionWidth, orderWidth := len(spec.Partition), len(spec.Order)
	ordered := WindowOrderState{values: make([]sqltype.Value, count*orderWidth)}
	if partitionWidth == 0 && orderWidth == 0 {
		rows := make([]int, count)
		for i := range rows {
			if err := ctx.Err(); err != nil {
				return ordered, err
			}
			rows[i] = i
		}
		ordered.partitions = []windowPartition{{rows: rows}}
		return ordered, nil
	}
	evaluation := state.Evaluation(ctx)
	scratch := make([]sqltype.Value, partitionWidth)
	buckets := make(map[uint64][]int)
	for row := range count {
		if err := ctx.Err(); err != nil {
			return ordered, err
		}
		evaluation.BindQueryRow(child.Source, child.Index(row))
		for k, key := range spec.Order {
			value, err := key.Expression.Eval(&evaluation)
			if err != nil {
				return ordered, err
			}
			if err := ValidateProducedValue(value, key.Expression.ResultUsage()); err != nil {
				return ordered, err
			}
			ordered.values[row*orderWidth+k] = value
		}
		hash := uint64(14695981039346656037)
		for k, key := range spec.Partition {
			value, err := key.Eval(&evaluation)
			if err != nil {
				return ordered, err
			}
			value, err = spec.Grouping[k].PrepareKey(ctx, value)
			if err != nil {
				return ordered, err
			}
			scratch[k] = value
			h, err := spec.Grouping[k].Hash(ctx, value)
			if err != nil {
				return ordered, err
			}
			hash = (hash ^ h) * 1099511628211
		}
		index := -1
		for _, candidate := range buckets[hash] {
			equal := true
			for k, family := range spec.Grouping {
				same, err := family.Equal(ctx, scratch[k], ordered.partitions[candidate].keys[k])
				if err != nil {
					return ordered, err
				}
				if !same {
					equal = false
					break
				}
			}
			if equal {
				index = candidate
				break
			}
		}
		if index < 0 {
			index = len(ordered.partitions)
			ordered.partitions = append(ordered.partitions, windowPartition{keys: append([]sqltype.Value(nil), scratch...)})
			buckets[hash] = append(buckets[hash], index)
		}
		ordered.partitions[index].rows = append(ordered.partitions[index].rows, row)
	}
	for _, partition := range ordered.partitions {
		if orderWidth > 0 {
			if err := MergeQueryIndices(ctx, partition.rows, func(a, b int) (int, error) {
				return CompareWindowRows(ctx, spec.Order, ordered.values, a, b, state.Input)
			}); err != nil {
				return ordered, err
			}
		}
	}
	return ordered, ctx.Err()
}

// A cursor advances monotonically over the half-open frame. NULL membership is
// ordering membership, not arithmetic: strict support never receives NULL keys.
func WindowFrameEndpoint(ctx context.Context, spec plan.WindowSpec, bound plan.WindowBound, offset sqltype.Value, ordered WindowOrderState, rows []int, peerStart, peerEnd, cursor int, upper bool, input sqltype.InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	switch bound.Kind {
	case parser.FrameUnboundedPreceding:
		return 0, nil
	case parser.FrameUnboundedFollowing:
		return len(rows), nil
	case parser.FrameCurrentRow:
		if upper {
			return peerEnd, nil
		}
		return peerStart, nil
	}
	key := spec.Order[0]
	width := len(spec.Order)
	base := ordered.values[rows[peerStart]*width]
	sub := bound.Kind == parser.FrameOffsetPreceding
	less := upper
	if key.Desc {
		sub = !sub
		less = !less
	}
	for cursor < len(rows) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		val := ordered.values[rows[cursor]*width]
		matches := false
		if val.IsNull() || base.IsNull() {
			if val.IsNull() && base.IsNull() {
				matches = true
			} else {
				before := val.IsNull() == key.NullsFirst
				matches = before == upper
			}
		} else {
			var err error
			matches, err = bound.Support.InRange(ctx, val, base, offset, sub, less, input)
			if err != nil {
				return 0, err
			}
		}
		if matches != upper {
			break
		}
		cursor++
	}
	return cursor, ctx.Err()
}

// GROUPS maps a peer ordinal to complete half-open row boundaries. Check the
// distance before arithmetic/conversion: even MaxInt64 offsets cannot wrap.
func WindowGroupsEndpoint(ctx context.Context, kind parser.FrameBoundKind, offset int64, peerEnds []int, group int, upper bool) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	count := len(peerEnds)
	switch kind {
	case parser.FrameUnboundedPreceding:
		return 0, nil
	case parser.FrameUnboundedFollowing:
		return peerEnds[count-1], nil
	case parser.FrameOffsetPreceding:
		if offset > int64(group) {
			return 0, nil
		}
		group -= int(offset)
	case parser.FrameOffsetFollowing:
		if offset >= int64(count-group) {
			return peerEnds[count-1], nil
		}
		group += int(offset)
	}
	if upper {
		return peerEnds[group], nil
	}
	if group == 0 {
		return 0, nil
	}
	return peerEnds[group-1], nil
}

// Physical endpoints are half-open indices. Compare available distances before
// converting or adding offsets, including MaxInt64, so arithmetic cannot overflow.
func WindowRowsEndpoint(ctx context.Context, kind parser.FrameBoundKind, offset int64, row, count int, upper bool) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	switch kind {
	case parser.FrameUnboundedPreceding:
		return 0, nil
	case parser.FrameUnboundedFollowing:
		return count, nil
	case parser.FrameOffsetPreceding:
		if offset > int64(row) {
			return 0, nil
		}
		row -= int(offset)
	case parser.FrameOffsetFollowing:
		if offset >= int64(count-row) {
			return count, nil
		}
		row += int(offset)
	}
	if upper {
		row++
	}
	return row, nil
}

func WindowFrameCount(ctx context.Context, usage sqltype.Usage, head, tail int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	// Both cursors are representable slice indices; their nonnegative distance
	// fits INT8. This is cardinality, not repeated ordinary aggregate transitions.
	return sqltype.Integer(usage.Type, int64(max(0, tail-head)))
}

// Exclusions only subtract from the unit provider's half-open membership.
func WindowMaskCount(mode parser.FrameExclusion, head, tail, current, peerStart, peerEnd int) int {
	if head >= tail {
		return 0
	}
	base := tail - head
	present := 0
	if head <= current && current < tail {
		present = 1
	}
	overlap := max(0, min(tail, peerEnd)-max(head, peerStart))
	switch mode {
	case parser.ExcludeCurrentRow:
		return base - present
	case parser.ExcludeGroup:
		return base - overlap
	case parser.ExcludeTies:
		return base - overlap + present
	}
	return base
}

func WindowMaskAdmits(mode parser.FrameExclusion, member, current, peerStart, peerEnd int) bool {
	switch mode {
	case parser.ExcludeCurrentRow:
		return member != current
	case parser.ExcludeGroup:
		return member < peerStart || member >= peerEnd
	case parser.ExcludeTies:
		return member == current || member < peerStart || member >= peerEnd
	}
	return true
}

// Every occurrence owns its cursors, private transition frames and saved final.
// A NULL inverse restarts only that occurrence, not siblings sharing membership.
type WindowAggregateState struct {
	CallIndex          int
	Aggregate          any
	Result             sqltype.Value
	values             []sqltype.Value
	head, next         int
	initialized, dirty bool
}

type WindowAggregates struct {
	States      []WindowAggregateState
	Initialized bool // Reset by the partition owner; entry cursors remain private.
}

func windowAggregateOperation(operation plan.WindowOperation) bool {
	return operation == plan.WindowSum || operation == plan.WindowAggregate
}

func (entry *WindowAggregateState) arguments(call *plan.WindowCall, evaluation *Context) error {
	count := 1
	if call.Operation == plan.WindowAggregate {
		count = len(call.Arguments)
	}
	if entry.values == nil {
		entry.values = make([]sqltype.Value, count)
	}
	for i := range count {
		argument := call.Argument
		if call.Operation == plan.WindowAggregate {
			argument = call.Arguments[i]
		}
		value, err := argument.Eval(evaluation)
		if err != nil {
			return err
		}
		if err = ValidateProducedValue(value, argument.ResultUsage()); err != nil {
			return err
		}
		entry.values[i] = value
	}
	return evaluation.Context.Err()
}

func (s *WindowAggregates) Evaluate(ctx context.Context, state *State, child *Stage, calls []plan.WindowCall, rows []int, frame plan.WindowFrame, head, tail, current, peerStart, peerEnd int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(s.States) == 0 {
		return nil
	}
	tail = max(head, tail)
	var evaluation Context
	evaluationReady := false
	begin := tail
	for i := range s.States {
		entry := &s.States[i]
		call := &calls[entry.CallIndex]
		entry.dirty = true
		moving, inverse := call.Provider.(*TransitionAggregate)
		inverse = inverse && moving.Moving
		restart := !s.Initialized || !entry.initialized || frame.Exclusion != parser.ExcludeNoOthers || head >= entry.next || tail < entry.next || head < entry.head || head != entry.head && !inverse
		if !restart && head == entry.head && tail == entry.next && current >= head && current < tail && (frame.End.Kind == parser.FrameUnboundedFollowing || frame.End.Kind == parser.FrameCurrentRow) {
			entry.dirty = false
			continue
		}
		if !evaluationReady {
			evaluation = state.Evaluation(ctx)
			evaluationReady = true
		}
		if !restart && head != entry.head {
			for member := entry.head; member < head; member++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				evaluation.BindQueryRow(child.Source, child.Index(rows[member]))
				if err := entry.arguments(call, &evaluation); err != nil {
					return err
				}
				punt, err := moving.Inverse(ctx, entry.Aggregate, entry.values, &evaluation)
				if err != nil {
					return err
				}
				if punt {
					restart = true
					break
				}
			}
		}
		if restart {
			var aggregate any
			var err error
			if contextual, ok := call.Provider.(functions.ExecutionAggregateProvider); ok {
				aggregate, err = contextual.InitWithContext(ctx, &evaluation)
			} else {
				aggregate, err = call.Provider.Init(ctx)
			}
			if err != nil {
				return err
			}
			entry.Aggregate, entry.next, entry.initialized = aggregate, head, true
		}
		entry.head = head
		begin = min(begin, entry.next)
	}
	// Bind source rows once and advance only occurrences requiring that member.
	// This preserves row-major volatile replay while a sibling can keep its state.
	for member := begin; member < tail; member++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !WindowMaskAdmits(frame.Exclusion, member, current, peerStart, peerEnd) {
			continue
		}
		evaluation.BindQueryRow(child.Source, child.Index(rows[member]))
		for i := range s.States {
			entry := &s.States[i]
			if !entry.dirty || member < entry.next {
				continue
			}
			call := &calls[entry.CallIndex]
			if err := entry.arguments(call, &evaluation); err != nil {
				return err
			}
			if err := call.Provider.Advance(ctx, entry.Aggregate, entry.values, &evaluation); err != nil {
				return err
			}
		}
	}
	for i := range s.States {
		entry := &s.States[i]
		if !entry.dirty {
			continue
		}
		call := &calls[entry.CallIndex]
		if err := ctx.Err(); err != nil {
			return err
		}
		var value sqltype.Value
		var err error
		if contextual, ok := call.Provider.(functions.ExecutionAggregateProvider); ok {
			value, err = contextual.FinalizeWithContext(ctx, entry.Aggregate, nil, &evaluation)
		} else {
			value, err = call.Provider.Finalize(ctx, entry.Aggregate)
		}
		if err != nil {
			return err
		}
		if err := ValidateProducedValue(value, call.Expression.ResultUsage()); err != nil {
			return err
		}
		entry.Result, entry.next = value, tail
	}
	s.Initialized = true
	return ctx.Err()
}

func openWindowNode(ctx context.Context, state *State, n *plan.WindowNode) (Stage, error) {
	// Mode admission belongs to the executor, before even an empty child's
	// expressions or any support routine can run.
	for _, call := range n.Calls {
		if call.AnalysisOnly {
			continue
		}
		if provider, ok := call.Provider.(*TransitionAggregate); ok {
			mode := provider.FinalModify
			if mode == "s" || mode == "w" {
				evaluation := state.Evaluation(ctx)
				identity, err := evaluation.Presentation().FormatRegprocedure(uint32(provider.Aggregate.Address().ObjectID))
				if err != nil {
					return Stage{}, err
				}
				return Stage{}, sqlError("0A000", "aggregate function "+identity+" does not support use as a window function")
			}
			evaluation := state.Evaluation(ctx)
			if err := provider.Start(&evaluation); err != nil {
				return Stage{}, err
			}
		}
	}
	child, err := OpenNode(ctx, state, n.Child)
	if err != nil {
		return Stage{}, err
	}
	defer child.Close()
	offsets := make([]windowFrameOffsets, len(n.Specs))
	const membership, validateBounds, peers uint8 = 1, 2, 4
	demanded := make([]uint8, len(n.Specs))
	ntileCount := 0
	for i, call := range n.Calls {
		if call.AnalysisOnly {
			continue
		}
		if call.Operation == plan.WindowNtile {
			ntileCount++
		}
		if call.Operation.Ranking() {
			demanded[n.SpecIndices[i]] |= peers
		}
		if call.Operation.FrameDependent() {
			demanded[n.SpecIndices[i]] |= membership | validateBounds
		}
		if call.Operation == plan.WindowLead || call.Operation == plan.WindowLag {
			demanded[n.SpecIndices[i]] |= validateBounds
		}
	}
	if err := child.Drain(ctx); err != nil {
		return Stage{}, err
	}
	orders := make([]WindowOrderState, len(n.Specs))
	for i := range n.Specs {
		if len(n.SortIndices) != 0 && n.SortIndices[i] != i {
			continue
		}
		// A pruned specification can still own the shared sort slot of a
		// surviving specification. Prepare that surviving, preflighted recipe.
		owner := i
		if !n.SpecActive(owner) {
			owner = -1
			for j := i + 1; j < len(n.Specs); j++ {
				if len(n.SortIndices) != 0 && n.SortIndices[j] == i && n.SpecActive(j) {
					owner = j
					break
				}
			}
		}
		if owner < 0 {
			continue
		}
		orders[i], err = prepareWindowOrder(ctx, state, &child, n.Specs[owner])
		if err != nil {
			return Stage{}, err
		}
	}
	for i, spec := range n.Specs {
		if demanded[i]&validateBounds != 0 {
			offsets[i], err = evaluateWindowOffsets(ctx, state, spec)
			if err != nil {
				return Stage{}, err
			}
		}
	}
	count, width := child.Count(), len(n.Calls)
	source := &Source{Input: child.Source, Authority: state.Source.Authority, Host: state.Source.Host, Window: true, WindowCount: count, windowWidth: width, WindowValues: make([]sqltype.Value, count*width), Scratch: make([]sqltype.Value, len(n.Schema()))}
	source.PresenceSlots, source.PresenceCount = child.Source.PresenceSlots, child.Source.PresenceCount
	if state.Source != child.Source {
		source.Sources = []*Source{state.Source}
	}
	if child.Selection != nil || child.Permutation != nil {
		source.WindowIndices = make([]int, count)
		for i := range count {
			source.WindowIndices[i] = child.Index(i)
		}
	}
	success := false
	defer func() {
		if !success {
			source.Close()
		}
	}()
	if count == 0 {
		if err := ctx.Err(); err != nil {
			return Stage{}, err
		}
		success = true
		return Stage{Source: source, Columns: n.Schema()}, nil
	}
	ntiles := make([]WindowNtileState, ntileCount)
	// Reuse one frame-local evaluator and its virtual-identity buffers across
	// actual value/ntile calls, rather than allocating them per member/output.
	var valueEvaluation *Context
	for _, call := range n.Calls {
		if call.AnalysisOnly {
			continue
		}
		if call.Operation.EvaluatesArgument() {
			evaluation := state.Evaluation(ctx)
			valueEvaluation = &evaluation
			break
		}
	}
	for specIndex, spec := range n.Specs {
		if !n.SpecActive(specIndex) {
			continue
		}
		sortIndex := specIndex
		if len(n.SortIndices) > 0 {
			sortIndex = n.SortIndices[specIndex]
		}
		ordered := &orders[sortIndex]
		sumCount := 0
		for callIndex, call := range n.Calls {
			if !call.AnalysisOnly && n.SpecIndices[callIndex] == specIndex && windowAggregateOperation(call.Operation) {
				sumCount++
			}
		}
		sums := WindowAggregates{States: make([]WindowAggregateState, 0, sumCount)}
		for callIndex, call := range n.Calls {
			if !call.AnalysisOnly && n.SpecIndices[callIndex] == specIndex && windowAggregateOperation(call.Operation) {
				sums.States = append(sums.States, WindowAggregateState{CallIndex: callIndex})
			}
		}
		for partitionIndex := range ordered.partitions {
			partition := &ordered.partitions[partitionIndex]
			sums.Initialized = false
			clear(ntiles)
			needsPeers := demanded[specIndex]&peers != 0 || demanded[specIndex]&membership != 0 && (spec.Frame.Unit != parser.FrameRows || spec.Frame.Exclusion == parser.ExcludeGroup || spec.Frame.Exclusion == parser.ExcludeTies)
			if needsPeers && len(spec.Order) > 0 && partition.peerEnds == nil {
				for start := 0; start < len(partition.rows); {
					end := start + 1
					for end < len(partition.rows) {
						sign, err := CompareWindowRows(ctx, spec.Order, ordered.values, partition.rows[start], partition.rows[end], state.Input)
						if err != nil {
							return Stage{}, err
						}
						if sign != 0 {
							break
						}
						end++
					}
					partition.peerEnds = append(partition.peerEnds, end)
					start = end
				}
			}
			head, tail, group, peerStart := 0, 0, 0, 0
			for i, row := range partition.rows {
				if err := ctx.Err(); err != nil {
					return Stage{}, err
				}
				peerEnd := len(partition.rows)
				if len(partition.peerEnds) > 0 {
					for i >= partition.peerEnds[group] {
						peerStart = partition.peerEnds[group]
						group++
					}
					peerEnd = partition.peerEnds[group]
				}
				if demanded[specIndex]&membership != 0 && (spec.Frame.Unit == parser.FrameRows || i == peerStart) {
					if spec.Frame.Unit == parser.FrameRows {
						head, err = WindowRowsEndpoint(ctx, spec.Frame.Start.Kind, offsets[specIndex].integerStart, i, len(partition.rows), false)
					} else if spec.Frame.Unit == parser.FrameGroups {
						head, err = WindowGroupsEndpoint(ctx, spec.Frame.Start.Kind, offsets[specIndex].integerStart, partition.peerEnds, group, false)
					} else {
						head, err = WindowFrameEndpoint(ctx, spec, spec.Frame.Start, offsets[specIndex].start, *ordered, partition.rows, peerStart, peerEnd, head, false, state.Input)
					}
					if err != nil {
						return Stage{}, err
					}
					if spec.Frame.Unit == parser.FrameRows {
						tail, err = WindowRowsEndpoint(ctx, spec.Frame.End.Kind, offsets[specIndex].integerEnd, i, len(partition.rows), true)
					} else if spec.Frame.Unit == parser.FrameGroups {
						tail, err = WindowGroupsEndpoint(ctx, spec.Frame.End.Kind, offsets[specIndex].integerEnd, partition.peerEnds, group, true)
					} else {
						tail, err = WindowFrameEndpoint(ctx, spec, spec.Frame.End, offsets[specIndex].end, *ordered, partition.rows, peerStart, peerEnd, tail, true, state.Input)
					}
					if err != nil {
						return Stage{}, err
					}
				}
				if err := sums.Evaluate(ctx, state, &child, n.Calls, partition.rows, spec.Frame, head, tail, i, peerStart, peerEnd); err != nil {
					return Stage{}, err
				}
				for _, entry := range sums.States {
					if err := ctx.Err(); err != nil {
						return Stage{}, err
					}
					source.WindowValues[row*width+entry.CallIndex] = entry.Result
				}
				ntileIndex := 0
				for callIndex, call := range n.Calls {
					if call.AnalysisOnly || n.SpecIndices[callIndex] != specIndex {
						continue
					}
					if windowAggregateOperation(call.Operation) {
						continue
					}
					if err := ctx.Err(); err != nil {
						return Stage{}, err
					}
					var value sqltype.Value
					switch call.Operation {
					case plan.WindowNumber:
						value, err = sqltype.Integer(call.Expression.ResultUsage().Type, int64(i)+1)
					case plan.WindowRank:
						value, err = sqltype.Integer(call.Expression.ResultUsage().Type, int64(peerStart)+1)
					case plan.WindowDenseRank:
						value, err = sqltype.Integer(call.Expression.ResultUsage().Type, int64(group)+1)
					case plan.WindowPercentRank:
						ratio := float64(0)
						if len(partition.rows) > 1 {
							ratio = float64(peerStart) / float64(len(partition.rows)-1)
						}
						value, err = sqltype.Float(call.Expression.ResultUsage().Type, ratio)
					case plan.WindowCumeDist:
						value, err = sqltype.Float(call.Expression.ResultUsage().Type, float64(peerEnd)/float64(len(partition.rows)))
					case plan.WindowNtile:
						value, err = ntiles[ntileIndex].evaluate(ctx, valueEvaluation, &child, call, partition.rows, i)
						ntileIndex++
					case plan.WindowLead, plan.WindowLag:
						value, err = evaluateWindowOffset(ctx, valueEvaluation, &child, call, partition.rows, i)
					case plan.WindowCount:
						value, err = WindowFrameCount(ctx, call.Expression.ResultUsage(), 0, WindowMaskCount(spec.Frame.Exclusion, head, tail, i, peerStart, peerEnd))
					case plan.WindowFirst, plan.WindowLast, plan.WindowNth:
						value, err = evaluateWindowValue(ctx, valueEvaluation, &child, call, partition.rows, spec.Frame, head, tail, i, peerStart, peerEnd)
					}
					if err != nil {
						return Stage{}, err
					}
					source.WindowValues[row*width+callIndex] = value
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Stage{}, err
	}
	success = true
	return Stage{Source: source, Columns: n.Schema()}, nil
}
