package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Every execution owns its dense occurrences and result overlay; the plan has no caches.
type windowNode struct {
	querySchema
	child                    LogicalNode
	specs                    []boundWindowSpec
	calls                    []windowCall
	specIndices, sortIndices []int
}
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
		evaluation.Row = child.source.row(child.index(row))
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

func openWindowNode(ctx context.Context, state *queryState, n *windowNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	defer child.Close()
	offsets := make([]windowFrameOffsets, len(n.specs))
	demanded := make([]bool, len(n.specs))
	for i, call := range n.calls {
		if !call.number {
			demanded[n.specIndices[i]] = true
		}
	}
	if err := child.drain(ctx); err != nil {
		return indexedStage{}, err
	}
	orders := make([]windowOrderState, len(n.specs))
	for i, spec := range n.specs {
		if len(n.sortIndices) == 0 || n.sortIndices[i] == i {
			orders[i], err = prepareWindowOrder(ctx, state, &child, spec)
			if err != nil {
				return indexedStage{}, err
			}
		}
	}
	for i, spec := range n.specs {
		if demanded[i] {
			offsets[i], err = evaluateWindowOffsets(ctx, state, spec)
			if err != nil {
				return indexedStage{}, err
			}
		}
	}
	count, width := child.count(), len(n.calls)
	source := &querySource{input: child.source, authority: state.source.authority, scope: state.source.scope, window: true, windowCount: count, windowWidth: width, windowValues: make([]sqltype.Value, count*width), scratch: make([]sqltype.Value, len(n.Schema()))}
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
		success = true
		return indexedStage{source: source, schema: n.Schema()}, nil
	}
	for specIndex, spec := range n.specs {
		sortIndex := specIndex
		if len(n.sortIndices) > 0 {
			sortIndex = n.sortIndices[specIndex]
		}
		ordered := &orders[sortIndex]
		for partitionIndex := range ordered.partitions {
			partition := &ordered.partitions[partitionIndex]
			if demanded[specIndex] && spec.frame.unit != parser.FrameRows && len(spec.order) > 0 && partition.peerEnds == nil {
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
			head, tail, peer := 0, 0, 0
			for start := 0; start < len(partition.rows); {
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
				end := len(partition.rows)
				group := peer
				if spec.frame.unit == parser.FrameRows {
					end = start + 1
				} else if len(partition.peerEnds) > 0 {
					end = partition.peerEnds[peer]
					peer++
				}
				var counted sqltype.Value
				if demanded[specIndex] {
					if spec.frame.unit == parser.FrameRows {
						head, err = windowRowsEndpoint(ctx, spec.frame.start.kind, offsets[specIndex].integerStart, start, len(partition.rows), false)
					} else if spec.frame.unit == parser.FrameGroups {
						head, err = windowGroupsEndpoint(ctx, spec.frame.start.kind, offsets[specIndex].integerStart, partition.peerEnds, group, false)
					} else {
						head, err = windowFrameEndpoint(ctx, spec, spec.frame.start, offsets[specIndex].start, *ordered, partition.rows, start, end, head, false, state.input)
					}
					if err != nil {
						return indexedStage{}, err
					}
					if spec.frame.unit == parser.FrameRows {
						tail, err = windowRowsEndpoint(ctx, spec.frame.end.kind, offsets[specIndex].integerEnd, start, len(partition.rows), true)
					} else if spec.frame.unit == parser.FrameGroups {
						tail, err = windowGroupsEndpoint(ctx, spec.frame.end.kind, offsets[specIndex].integerEnd, partition.peerEnds, group, true)
					} else {
						tail, err = windowFrameEndpoint(ctx, spec, spec.frame.end, offsets[specIndex].end, *ordered, partition.rows, start, end, tail, true, state.input)
					}
					if err != nil {
						return indexedStage{}, err
					}
					counted, err = windowFrameCount(ctx, n.calls[0].expression.ResultUsage(), head, tail)
					if err != nil {
						return indexedStage{}, err
					}
				}
				for i := start; i < end; i++ {
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
					row := partition.rows[i]
					for callIndex, call := range n.calls {
						if n.specIndices[callIndex] != specIndex {
							continue
						}
						value := counted
						if call.number {
							value, err = sqltype.Integer(call.expression.ResultUsage().Type, int64(i)+1)
							if err != nil {
								return indexedStage{}, err
							}
						}
						source.windowValues[row*width+callIndex] = value
					}
				}
				start = end
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	success = true
	return indexedStage{source: source, schema: n.Schema()}, nil
}
