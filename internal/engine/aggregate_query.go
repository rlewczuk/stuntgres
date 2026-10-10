package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type aggregateNode struct {
	querySchema
	child        LogicalNode
	keys         []BoundExpr
	grouping     []sqltype.Grouping
	calls        []aggregateCall
	presenceOnly bool // Retain analyzed grouping while discarding eligible EXISTS demand.
}
type queryGroup struct {
	keys     []sqltype.Value
	states   []any
	ordered  [][]sqltype.Value
	distinct []aggregateDistinctState
}

type aggregateDistinctState struct {
	buckets map[uint64][]int
	tuples  []sqltype.Value
}

func (s *aggregateDistinctState) admit(ctx context.Context, grouping []sqltype.Grouping, tuple []sqltype.Value) (bool, error) {
	hash := uint64(14695981039346656037)
	for i, family := range grouping {
		value, err := family.PrepareKey(ctx, tuple[i])
		if err != nil {
			return false, err
		}
		tuple[i] = value
		h, err := family.Hash(ctx, value)
		if err != nil {
			return false, err
		}
		hash = (hash ^ h) * 1099511628211
	}
	index, err := findDistinctTuple(ctx, grouping, tuple, s.tuples, s.buckets[hash])
	if err != nil || index >= 0 {
		return false, err
	}
	if s.buckets == nil {
		s.buckets = make(map[uint64][]int)
	}
	s.buckets[hash] = append(s.buckets[hash], len(s.tuples)/len(grouping))
	s.tuples = append(s.tuples, tuple...)
	return true, ctx.Err()
}

func openAggregateNode(ctx context.Context, state *queryState, n *aggregateNode) (indexedStage, error) {
	if n.presenceOnly {
		return openQueryNode(ctx, state, n.child)
	}
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	child, err = settlePendingQueryMarks(ctx, state, n.child, child)
	if err != nil {
		return indexedStage{}, err
	}
	defer child.Close()
	if err := child.drain(ctx); err != nil {
		return indexedStage{}, err
	}
	groups := make([]queryGroup, 0)
	var buckets map[uint64][]int
	if len(n.keys) > 0 {
		buckets = make(map[uint64][]int)
	}
	scratch := make([]sqltype.Value, len(n.keys))
	orderedWidth := 0
	hasDistinct := false
	for _, call := range n.calls {
		hasDistinct = hasDistinct || len(call.distinct) > 0
		if call.stageWidth > orderedWidth {
			orderedWidth = call.stageWidth
		}
	}
	tuple := make([]sqltype.Value, orderedWidth)
	newGroup := func() (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		group := queryGroup{keys: make([]sqltype.Value, len(n.Schema())), states: make([]any, len(n.calls))}
		if orderedWidth > 0 {
			group.ordered = make([][]sqltype.Value, len(n.calls))
		}
		if hasDistinct {
			group.distinct = make([]aggregateDistinctState, len(n.calls))
		}
		copy(group.keys, scratch)
		for i, call := range n.calls {
			var err error
			group.states[i], err = call.provider.Init(ctx)
			if err != nil {
				return 0, err
			}
			if err = ctx.Err(); err != nil {
				return 0, err
			}
		}
		groups = append(groups, group)
		return len(groups) - 1, nil
	}
	if len(n.keys) == 0 {
		if _, err := newGroup(); err != nil {
			return indexedStage{}, err
		}
	}
	evaluation := state.evaluation(ctx)
	for i := range child.count() {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		evaluation.Row = child.source.row(child.index(i))
		hash := uint64(14695981039346656037)
		for k, key := range n.keys {
			value, err := key.Eval(&evaluation)
			if err != nil {
				return indexedStage{}, err
			}
			if err = ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			value, err = n.grouping[k].PrepareKey(ctx, value)
			if err != nil {
				return indexedStage{}, err
			}
			scratch[k] = value
			h, err := n.grouping[k].Hash(ctx, value)
			if err != nil {
				return indexedStage{}, err
			}
			hash = (hash ^ h) * 1099511628211
		}
		index := 0
		if len(n.keys) > 0 {
			index, err = findQueryGroup(ctx, n.grouping, scratch, groups, buckets[hash])
			if err != nil {
				return indexedStage{}, err
			}
			if index < 0 {
				index, err = newGroup()
				if err != nil {
					return indexedStage{}, err
				}
				buckets[hash] = append(buckets[hash], index)
			}
		}
		for p, call := range n.calls {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			if call.filter != nil {
				filter, err := call.filter.Eval(&evaluation)
				if err != nil {
					return indexedStage{}, err
				}
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
				if filter.IsNull() {
					continue
				}
				admit, err := filter.Boolean()
				if err != nil {
					return indexedStage{}, err
				}
				if !admit {
					continue
				}
			}
			var value sqltype.Value
			if call.argument != nil {
				var err error
				value, err = call.argument.Eval(&evaluation)
				if err != nil {
					return indexedStage{}, err
				}
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
			}
			var separator sqltype.Value
			if call.separator != nil {
				separator, err = call.separator.Eval(&evaluation)
				if err != nil {
					return indexedStage{}, err
				}
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
			}
			if len(call.order) > 0 {
				tuple[0], tuple[1] = value, separator
				for _, key := range call.order {
					if key.slot < 0 {
						tuple[key.cache], err = key.expression.Eval(&evaluation)
						if err != nil {
							return indexedStage{}, err
						}
					}
					if key.slot < 0 && tuple[key.cache].Type() != key.expression.ResultUsage().Type {
						return indexedStage{}, fmt.Errorf("stuntgres: invalid aggregate ordering value")
					}
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
				}
			}
			if len(call.distinct) > 0 {
				scratch := [2]sqltype.Value{value, separator}
				admit, err := groups[index].distinct[p].admit(ctx, call.distinct, scratch[:len(call.distinct)])
				if err != nil {
					return indexedStage{}, err
				}
				if !admit {
					continue
				}
			}
			if len(call.order) > 0 {
				if call.argument == nil || !value.IsNull() {
					groups[index].ordered[p] = append(groups[index].ordered[p], tuple[:call.stageWidth]...)
				}
				continue
			}
			if call.argument != nil && value.IsNull() {
				continue
			}
			if err := call.provider.Advance(ctx, groups[index].states[p], value, separator); err != nil {
				return indexedStage{}, err
			}
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
		}
	}
	rows := make([][]sqltype.Value, len(groups))
	for i, group := range groups {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		row := group.keys
		for p, call := range n.calls {
			if len(call.order) > 0 {
				if err := replayOrderedAggregate(ctx, state, call, group.states[p], group.ordered[p]); err != nil {
					return indexedStage{}, err
				}
			}
			value, err := call.provider.Finalize(ctx, group.states[p])
			if err != nil {
				return indexedStage{}, err
			}
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			if value.Type() != call.provider.ResultUsage().Type {
				return indexedStage{}, fmt.Errorf("stuntgres: invalid finalized aggregate value")
			}
			row[len(n.keys)+p] = value
		}
		rows[i] = row
	}
	// A nested aggregate borrows the captured input occurrence. Its grouped
	// layout must not replace or close that shared source (or an outer frame).
	source := &querySource{anonymous: rows, materialized: true, flatCount: len(rows), scope: child.source.scope, locks: child.source.locks}
	if err := child.Close(); err != nil {
		return indexedStage{}, err
	}
	return indexedStage{source: source, schema: n.Schema()}, nil
}

func findQueryGroup(ctx context.Context, grouping []sqltype.Grouping, keys []sqltype.Value, groups []queryGroup, candidates []int) (int, error) {
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		equal := true
		for k, value := range keys {
			same, err := grouping[k].Equal(ctx, value, groups[candidate].keys[k])
			if err != nil {
				return -1, err
			}
			if !same {
				equal = false
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		if equal {
			return candidate, nil
		}
	}
	return -1, ctx.Err()
}

func replayOrderedAggregate(ctx context.Context, state *queryState, call aggregateCall, aggregateState any, tuples []sqltype.Value) error {
	width := call.stageWidth
	indices := make([]int, len(tuples)/width)
	for i := range indices {
		indices[i] = i
	}
	if err := mergeQueryIndices(ctx, indices, func(a, b int) (int, error) {
		for _, key := range call.order {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			left, right := tuples[a*width+key.cache], tuples[b*width+key.cache]
			if left.IsNull() && right.IsNull() {
				continue
			}
			if left.IsNull() || right.IsNull() {
				if left.IsNull() == key.nullsFirst {
					return -1, nil
				}
				return 1, nil
			}
			sign, err := key.comparison.Compare(ctx, left, right, state.input)
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
	}); err != nil {
		return err
	}
	for _, index := range indices {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := call.provider.Advance(ctx, aggregateState, tuples[index*width], tuples[index*width+1]); err != nil {
			return err
		}
	}
	return ctx.Err()
}
