package engine

import (
	"context"
	"stuntgres/internal/sqltype"
)

// Every execution owns its dense occurrences and result overlay; the plan has no caches.
type windowNode struct {
	querySchema
	child       LogicalNode
	specs       []boundWindowSpec
	calls       []windowCall
	specIndices []int
}
type windowPartition struct {
	keys []sqltype.Value
	rows []int
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
func openWindowNode(ctx context.Context, state *queryState, n *windowNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	defer child.Close()
	if err := child.drain(ctx); err != nil {
		return indexedStage{}, err
	}
	count := child.count()
	width := len(n.calls)
	source := &querySource{input: child.source, window: true, windowCount: count, windowWidth: width, windowValues: make([]sqltype.Value, count*width), scratch: make([]sqltype.Value, len(n.Schema()))}
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
	evaluation := state.evaluation(ctx)
	for specIndex, spec := range n.specs {
		partitionWidth, orderWidth := len(spec.partition), len(spec.order)
		needsCount := false
		for i, call := range n.calls {
			if n.specIndices[i] == specIndex && !call.number {
				needsCount = true
			}
		}
		provider := countProvider{usage: n.calls[0].expression.ResultUsage()}
		if partitionWidth == 0 && orderWidth == 0 {
			var counted sqltype.Value
			if needsCount {
				accumulator, err := provider.Init(ctx)
				if err != nil {
					return indexedStage{}, err
				}
				for range count {
					if err := provider.Advance(ctx, accumulator, sqltype.Value{}, sqltype.Value{}); err != nil {
						return indexedStage{}, err
					}
				}
				counted, err = provider.Finalize(ctx, accumulator)
				if err != nil {
					return indexedStage{}, err
				}
			}
			for row := range count {
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
				for callIndex, call := range n.calls {
					if n.specIndices[callIndex] != specIndex {
						continue
					}
					value := counted
					if call.number {
						var err error
						value, err = sqltype.Integer(call.expression.ResultUsage().Type, int64(row)+1)
						if err != nil {
							return indexedStage{}, err
						}
					}
					source.windowValues[row*width+callIndex] = value
				}
			}
			continue
		}
		orderValues := make([]sqltype.Value, count*orderWidth)
		scratch := make([]sqltype.Value, partitionWidth)
		partitions := make([]windowPartition, 0)
		buckets := make(map[uint64][]int)
		for row := range count {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			evaluation.Row = child.source.row(child.index(row))
			for k, key := range spec.order {
				value, err := key.expression.Eval(&evaluation)
				if err != nil {
					return indexedStage{}, err
				}
				if err := validateProducedValue(value, key.expression.ResultUsage()); err != nil {
					return indexedStage{}, err
				}
				orderValues[row*orderWidth+k] = value
			}
			hash := uint64(14695981039346656037)
			for k, key := range spec.partition {
				value, err := key.Eval(&evaluation)
				if err != nil {
					return indexedStage{}, err
				}
				value, err = spec.grouping[k].PrepareKey(ctx, value)
				if err != nil {
					return indexedStage{}, err
				}
				scratch[k] = value
				h, err := spec.grouping[k].Hash(ctx, value)
				if err != nil {
					return indexedStage{}, err
				}
				hash = (hash ^ h) * 1099511628211
			}
			index := -1
			for _, candidate := range buckets[hash] {
				equal := true
				for k, family := range spec.grouping {
					same, err := family.Equal(ctx, scratch[k], partitions[candidate].keys[k])
					if err != nil {
						return indexedStage{}, err
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
				index = len(partitions)
				partitions = append(partitions, windowPartition{keys: append([]sqltype.Value(nil), scratch...)})
				buckets[hash] = append(buckets[hash], index)
			}
			partitions[index].rows = append(partitions[index].rows, row)
		}
		for _, partition := range partitions {
			if len(spec.order) > 0 {
				err := mergeQueryIndices(ctx, partition.rows, func(a, b int) (int, error) { return compareWindowRows(ctx, spec.order, orderValues, a, b, state.input) })
				if err != nil {
					return indexedStage{}, err
				}
			}
			var countState any
			if needsCount {
				var err error
				countState, err = provider.Init(ctx)
				if err != nil {
					return indexedStage{}, err
				}
			}
			for start := 0; start < len(partition.rows); {
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
				end := len(partition.rows)
				if len(spec.order) > 0 {
					end = start + 1
					for end < len(partition.rows) {
						sign, err := compareWindowRows(ctx, spec.order, orderValues, partition.rows[start], partition.rows[end], state.input)
						if err != nil {
							return indexedStage{}, err
						}
						if sign != 0 {
							break
						}
						end++
					}
				}
				var counted sqltype.Value
				if needsCount {
					for i := start; i < end; i++ {
						if err := provider.Advance(ctx, countState, sqltype.Value{}, sqltype.Value{}); err != nil {
							return indexedStage{}, err
						}
					}
					var err error
					counted, err = provider.Finalize(ctx, countState)
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
							var err error
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
