package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type windowPartition struct {
	keys           []sqltype.Value
	rows, peerEnds []int
}
type windowOrderState struct {
	values     []sqltype.Value
	partitions []windowPartition
}
type windowFrameOffsets struct {
	start, end               sqltype.Value
	integerStart, integerEnd int64
}

func compareWindowRows(ctx context.Context, keys []sortKey, values []sqltype.Value, a, b int, input sqltype.InputEnvironment) (int, error) {
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
			if left.IsNull() == key.nullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		sign, err := key.comparison.Compare(ctx, left, right, input)
		if err != nil {
			return 0, err
		}
		if sign != 0 {
			if key.desc {
				sign = -sign
			}
			return sign, nil
		}
	}
	return 0, ctx.Err()
}

func evaluateWindowOffsets(ctx context.Context, state *queryState, spec boundWindowSpec) (windowFrameOffsets, error) {
	var values windowFrameOffsets
	evaluation := state.evaluation(ctx)
	for i, bound := range [2]boundWindowBound{spec.frame.start, spec.frame.end} {
		if bound.offset == nil {
			continue
		}
		value, err := bound.offset.Eval(&evaluation)
		if err != nil {
			return values, err
		}
		if err := validateProducedValue(value, bound.offset.ResultUsage()); err != nil {
			return values, err
		}
		if value.IsNull() {
			word := "starting"
			if i == 1 {
				word = "ending"
			}
			return values, sqlError("22004", "frame "+word+" offset must not be null")
		}
		if spec.frame.unit == parser.FrameGroups || spec.frame.unit == parser.FrameRows {
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

func prepareWindowOrder(ctx context.Context, state *queryState, child *indexedStage, spec boundWindowSpec) (windowOrderState, error) {
	count := child.count()
	partitionWidth, orderWidth := len(spec.partition), len(spec.order)
	ordered := windowOrderState{values: make([]sqltype.Value, count*orderWidth)}
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
	evaluation := state.evaluation(ctx)
	scratch := make([]sqltype.Value, partitionWidth)
	buckets := make(map[uint64][]int)
	for row := range count {
		if err := ctx.Err(); err != nil {
			return ordered, err
		}
		evaluation.bindQueryRow(child.source, child.index(row))
		for k, key := range spec.order {
			value, err := key.expression.Eval(&evaluation)
			if err != nil {
				return ordered, err
			}
			if err := validateProducedValue(value, key.expression.ResultUsage()); err != nil {
				return ordered, err
			}
			ordered.values[row*orderWidth+k] = value
		}
		hash := uint64(14695981039346656037)
		for k, key := range spec.partition {
			value, err := key.Eval(&evaluation)
			if err != nil {
				return ordered, err
			}
			value, err = spec.grouping[k].PrepareKey(ctx, value)
			if err != nil {
				return ordered, err
			}
			scratch[k] = value
			h, err := spec.grouping[k].Hash(ctx, value)
			if err != nil {
				return ordered, err
			}
			hash = (hash ^ h) * 1099511628211
		}
		index := -1
		for _, candidate := range buckets[hash] {
			equal := true
			for k, family := range spec.grouping {
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
			if err := mergeQueryIndices(ctx, partition.rows, func(a, b int) (int, error) {
				return compareWindowRows(ctx, spec.order, ordered.values, a, b, state.input)
			}); err != nil {
				return ordered, err
			}
		}
	}
	return ordered, ctx.Err()
}

// A cursor advances monotonically over the half-open frame. NULL membership is
// ordering membership, not arithmetic: strict support never receives NULL keys.
func windowFrameEndpoint(ctx context.Context, spec boundWindowSpec, bound boundWindowBound, offset sqltype.Value, ordered windowOrderState, rows []int, peerStart, peerEnd, cursor int, upper bool, input sqltype.InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	switch bound.kind {
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
	key := spec.order[0]
	width := len(spec.order)
	base := ordered.values[rows[peerStart]*width]
	sub := bound.kind == parser.FrameOffsetPreceding
	less := upper
	if key.desc {
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
				before := val.IsNull() == key.nullsFirst
				matches = before == upper
			}
		} else {
			var err error
			matches, err = bound.support.InRange(ctx, val, base, offset, sub, less, input)
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
func windowGroupsEndpoint(ctx context.Context, kind parser.FrameBoundKind, offset int64, peerEnds []int, group int, upper bool) (int, error) {
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
func windowRowsEndpoint(ctx context.Context, kind parser.FrameBoundKind, offset int64, row, count int, upper bool) (int, error) {
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

func windowFrameCount(ctx context.Context, usage sqltype.Usage, head, tail int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	// Both cursors are representable slice indices; their nonnegative distance
	// fits INT8. This is cardinality, not repeated ordinary aggregate transitions.
	return sqltype.Integer(usage.Type, int64(max(0, tail-head)))
}

// Exclusions only subtract from the unit provider's half-open membership.
func windowMaskCount(mode parser.FrameExclusion, head, tail, current, peerStart, peerEnd int) int {
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

func windowMaskAdmits(mode parser.FrameExclusion, member, current, peerStart, peerEnd int) bool {
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

// Transition and saved results belong to one execution and one partition, never
// to the bound calls. Each SUM occurrence keeps its own provider state.
type windowSumState struct {
	callIndex int
	aggregate any
	result    sqltype.Value
}

type windowSums struct {
	states      []windowSumState
	initialized bool
	head, next  int
}

func (s *windowSums) evaluate(ctx context.Context, state *queryState, child *indexedStage, calls []windowCall, rows []int, frame boundWindowFrame, head, tail, current, peerStart, peerEnd int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(s.states) == 0 {
		return nil
	}
	// Empty/reversed membership has no transitions, and cannot retain an old
	// nonempty aggregate. Existing providers have no inverse transition.
	tail = max(head, tail)
	restart := !s.initialized || frame.exclusion != parser.ExcludeNoOthers || head != s.head || head >= s.next || tail < s.next
	if !restart && tail == s.next && current >= head && current < s.next && (frame.end.kind == parser.FrameUnboundedFollowing || frame.end.kind == parser.FrameCurrentRow) {
		return ctx.Err()
	}
	if restart {
		for i := range s.states {
			entry := &s.states[i]
			aggregate, err := calls[entry.callIndex].provider.Init(ctx)
			if err != nil {
				return err
			}
			entry.aggregate = aggregate
		}
		s.head, s.next, s.initialized = head, head, true
	}
	evaluation := state.evaluation(ctx)
	// Bind each admitted member once, then advance every independent occurrence.
	// Call-major replay changes volatile values even when the total is unchanged.
	for member := s.next; member < tail; member++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !windowMaskAdmits(frame.exclusion, member, current, peerStart, peerEnd) {
			continue
		}
		evaluation.bindQueryRow(child.source, child.index(rows[member]))
		for i := range s.states {
			entry := &s.states[i]
			call := &calls[entry.callIndex]
			if err := ctx.Err(); err != nil {
				return err
			}
			value, err := call.argument.Eval(&evaluation)
			if err != nil {
				return err
			}
			if err := validateProducedValue(value, call.argument.ResultUsage()); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := call.provider.Advance(ctx, entry.aggregate, value, sqltype.Value{}); err != nil {
				return err
			}
		}
	}
	s.next = tail
	for i := range s.states {
		entry := &s.states[i]
		call := &calls[entry.callIndex]
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := call.provider.Finalize(ctx, entry.aggregate)
		if err != nil {
			return err
		}
		if err := validateProducedValue(value, call.expression.ResultUsage()); err != nil {
			return err
		}
		entry.result = value
	}
	return ctx.Err()
}

func openWindowNode(ctx context.Context, state *queryState, n *windowNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	defer child.Close()
	offsets := make([]windowFrameOffsets, len(n.specs))
	const membership, validateBounds, peers uint8 = 1, 2, 4
	demanded := make([]uint8, len(n.specs))
	ntileCount := 0
	for i, call := range n.calls {
		if call.analysisOnly {
			continue
		}
		if call.operation == windowNtile {
			ntileCount++
		}
		if call.operation.ranking() {
			demanded[n.specIndices[i]] |= peers
		}
		if call.operation.frameDependent() {
			demanded[n.specIndices[i]] |= membership | validateBounds
		}
		if call.operation == windowLead || call.operation == windowLag {
			demanded[n.specIndices[i]] |= validateBounds
		}
	}
	if err := child.drain(ctx); err != nil {
		return indexedStage{}, err
	}
	orders := make([]windowOrderState, len(n.specs))
	for i := range n.specs {
		if len(n.sortIndices) != 0 && n.sortIndices[i] != i {
			continue
		}
		// A pruned specification can still own the shared sort slot of a
		// surviving specification. Prepare that surviving, preflighted recipe.
		owner := i
		if !n.specActive(owner) {
			owner = -1
			for j := i + 1; j < len(n.specs); j++ {
				if len(n.sortIndices) != 0 && n.sortIndices[j] == i && n.specActive(j) {
					owner = j
					break
				}
			}
		}
		if owner < 0 {
			continue
		}
		orders[i], err = prepareWindowOrder(ctx, state, &child, n.specs[owner])
		if err != nil {
			return indexedStage{}, err
		}
	}
	for i, spec := range n.specs {
		if demanded[i]&validateBounds != 0 {
			offsets[i], err = evaluateWindowOffsets(ctx, state, spec)
			if err != nil {
				return indexedStage{}, err
			}
		}
	}
	count, width := child.count(), len(n.calls)
	source := &querySource{input: child.source, authority: state.source.authority, scope: state.source.scope, window: true, windowCount: count, windowWidth: width, windowValues: make([]sqltype.Value, count*width), scratch: make([]sqltype.Value, len(n.Schema()))}
	source.presenceSlots, source.presenceCount = child.source.presenceSlots, child.source.presenceCount
	if state.source != child.source {
		source.sources = []*querySource{state.source}
	}
	if child.selection != nil || child.permutation != nil {
		source.windowIndices = make([]int, count)
		for i := range count {
			source.windowIndices[i] = child.index(i)
		}
	}
	success := false
	defer func() {
		if !success {
			source.close()
		}
	}()
	if count == 0 {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		success = true
		return indexedStage{source: source, schema: n.Schema()}, nil
	}
	ntiles := make([]windowNtileState, ntileCount)
	// Reuse one frame-local evaluator and its virtual-identity buffers across
	// actual value/ntile calls, rather than allocating them per member/output.
	var valueEvaluation *EvalContext
	for _, call := range n.calls {
		if call.analysisOnly {
			continue
		}
		if call.operation.evaluatesArgument() {
			evaluation := state.evaluation(ctx)
			valueEvaluation = &evaluation
			break
		}
	}
	for specIndex, spec := range n.specs {
		if !n.specActive(specIndex) {
			continue
		}
		sortIndex := specIndex
		if len(n.sortIndices) > 0 {
			sortIndex = n.sortIndices[specIndex]
		}
		ordered := &orders[sortIndex]
		sumCount := 0
		for callIndex, call := range n.calls {
			if !call.analysisOnly && n.specIndices[callIndex] == specIndex && call.operation == windowSum {
				sumCount++
			}
		}
		sums := windowSums{states: make([]windowSumState, 0, sumCount)}
		for callIndex, call := range n.calls {
			if !call.analysisOnly && n.specIndices[callIndex] == specIndex && call.operation == windowSum {
				sums.states = append(sums.states, windowSumState{callIndex: callIndex})
			}
		}
		for partitionIndex := range ordered.partitions {
			partition := &ordered.partitions[partitionIndex]
			sums.initialized = false
			clear(ntiles)
			needsPeers := demanded[specIndex]&peers != 0 || demanded[specIndex]&membership != 0 && (spec.frame.unit != parser.FrameRows || spec.frame.exclusion == parser.ExcludeGroup || spec.frame.exclusion == parser.ExcludeTies)
			if needsPeers && len(spec.order) > 0 && partition.peerEnds == nil {
				for start := 0; start < len(partition.rows); {
					end := start + 1
					for end < len(partition.rows) {
						sign, err := compareWindowRows(ctx, spec.order, ordered.values, partition.rows[start], partition.rows[end], state.input)
						if err != nil {
							return indexedStage{}, err
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
					return indexedStage{}, err
				}
				peerEnd := len(partition.rows)
				if len(partition.peerEnds) > 0 {
					for i >= partition.peerEnds[group] {
						peerStart = partition.peerEnds[group]
						group++
					}
					peerEnd = partition.peerEnds[group]
				}
				if demanded[specIndex]&membership != 0 && (spec.frame.unit == parser.FrameRows || i == peerStart) {
					if spec.frame.unit == parser.FrameRows {
						head, err = windowRowsEndpoint(ctx, spec.frame.start.kind, offsets[specIndex].integerStart, i, len(partition.rows), false)
					} else if spec.frame.unit == parser.FrameGroups {
						head, err = windowGroupsEndpoint(ctx, spec.frame.start.kind, offsets[specIndex].integerStart, partition.peerEnds, group, false)
					} else {
						head, err = windowFrameEndpoint(ctx, spec, spec.frame.start, offsets[specIndex].start, *ordered, partition.rows, peerStart, peerEnd, head, false, state.input)
					}
					if err != nil {
						return indexedStage{}, err
					}
					if spec.frame.unit == parser.FrameRows {
						tail, err = windowRowsEndpoint(ctx, spec.frame.end.kind, offsets[specIndex].integerEnd, i, len(partition.rows), true)
					} else if spec.frame.unit == parser.FrameGroups {
						tail, err = windowGroupsEndpoint(ctx, spec.frame.end.kind, offsets[specIndex].integerEnd, partition.peerEnds, group, true)
					} else {
						tail, err = windowFrameEndpoint(ctx, spec, spec.frame.end, offsets[specIndex].end, *ordered, partition.rows, peerStart, peerEnd, tail, true, state.input)
					}
					if err != nil {
						return indexedStage{}, err
					}
				}
				if err := sums.evaluate(ctx, state, &child, n.calls, partition.rows, spec.frame, head, tail, i, peerStart, peerEnd); err != nil {
					return indexedStage{}, err
				}
				for _, entry := range sums.states {
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
					source.windowValues[row*width+entry.callIndex] = entry.result
				}
				ntileIndex := 0
				for callIndex, call := range n.calls {
					if call.analysisOnly || n.specIndices[callIndex] != specIndex {
						continue
					}
					if call.operation == windowSum {
						continue
					}
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
					var value sqltype.Value
					switch call.operation {
					case windowNumber:
						value, err = sqltype.Integer(call.expression.ResultUsage().Type, int64(i)+1)
					case windowRank:
						value, err = sqltype.Integer(call.expression.ResultUsage().Type, int64(peerStart)+1)
					case windowDenseRank:
						value, err = sqltype.Integer(call.expression.ResultUsage().Type, int64(group)+1)
					case windowPercentRank:
						ratio := float64(0)
						if len(partition.rows) > 1 {
							ratio = float64(peerStart) / float64(len(partition.rows)-1)
						}
						value, err = sqltype.Float(call.expression.ResultUsage().Type, ratio)
					case windowCumeDist:
						value, err = sqltype.Float(call.expression.ResultUsage().Type, float64(peerEnd)/float64(len(partition.rows)))
					case windowNtile:
						value, err = ntiles[ntileIndex].evaluate(ctx, valueEvaluation, &child, call, partition.rows, i)
						ntileIndex++
					case windowLead, windowLag:
						value, err = evaluateWindowOffset(ctx, valueEvaluation, &child, call, partition.rows, i)
					case windowCount:
						value, err = windowFrameCount(ctx, call.expression.ResultUsage(), 0, windowMaskCount(spec.frame.exclusion, head, tail, i, peerStart, peerEnd))
					case windowFirst, windowLast, windowNth:
						value, err = evaluateWindowValue(ctx, valueEvaluation, &child, call, partition.rows, spec.frame, head, tail, i, peerStart, peerEnd)
					}
					if err != nil {
						return indexedStage{}, err
					}
					source.windowValues[row*width+callIndex] = value
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	success = true
	return indexedStage{source: source, schema: n.Schema()}, nil
}
