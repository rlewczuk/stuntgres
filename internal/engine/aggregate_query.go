package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type aggregateNode struct {
	querySchema
	child          LogicalNode
	keys           []BoundExpr
	grouping       []queryGrouping
	calls          []aggregateCall
	sets           [][]int // Canonical key IDs; each occurrence has isolated state.
	groupingActive bool
	presenceOnly   bool // Retain analyzed grouping while discarding eligible EXISTS demand.
}

func (n *aggregateNode) discardableGrouping() bool {
	if len(n.calls) != 0 || n.groupingActive {
		return false
	}
	if n.sets == nil {
		return len(n.keys) > 0
	}
	return len(n.sets) == 1 && len(n.sets[0]) > 0
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

func (s *aggregateDistinctState) admit(ctx context.Context, grouping []queryGrouping, tuple []sqltype.Value) (bool, error) {
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
	sets := n.sets
	if sets == nil {
		keys := make([]int, len(n.keys))
		for i := range keys {
			keys[i] = i
		}
		sets = [][]int{keys}
	}
	groups := make([]queryGroup, 0)
	buckets := make([]map[uint64][]int, len(sets))
	indices := make([]int, len(sets))
	for i := range indices {
		indices[i] = -1
	}
	scratch := make([]sqltype.Value, len(n.keys))
	hashes := make([]uint64, len(n.keys))
	orderedWidth := 0
	hasDistinct := false
	directWidth := 0
	for _, call := range n.calls {
		hasDistinct = hasDistinct || len(call.distinct) > 0
		if len(call.direct) > directWidth {
			directWidth = len(call.direct)
		}
		if call.stageWidth > orderedWidth {
			orderedWidth = call.stageWidth
		}
	}
	tuple := make([]sqltype.Value, orderedWidth)
	// Reserved ordinary payload slots keep the hypothetical target in the
	// same layout as retained input tuples. Reused only after finalization.
	var directLocal [2]sqltype.Value
	directValues := directLocal[:]
	if directWidth > 0 {
		directValues = make([]sqltype.Value, 2+directWidth)
	}
	evaluation := state.evaluation(ctx)
	output := evaluation.Output
	scope := evaluation.liveScope()
	output.Regclass, output.Regtype, output.Regproc = scope, scope, scope
	output.Regrole = scope
	output.Operator = scope
	output.Regcollation, output.Regnamespace = scope, scope
	output.RegTS = scope
	newGroup := func(occurrence int) (int, error) {
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
		for k, key := range n.keys {
			group.keys[k] = sqltype.Null(key.ResultUsage().Type)
		}
		for _, k := range sets[occurrence] {
			group.keys[k] = scratch[k]
		}
		if slot := len(n.keys) + len(n.calls); slot < len(group.keys) {
			value, err := sqltype.Integer(n.Schema()[slot].Usage.Type, int64(occurrence))
			if err != nil {
				return 0, err
			}
			group.keys[slot] = value
		}
		for i, call := range n.calls {
			var err error
			if provider, ok := call.provider.(contextualAggregateProvider); ok {
				group.states[i], err = provider.InitWithEnvironment(ctx, output)
			} else {
				group.states[i], err = call.provider.Init(ctx)
			}
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
	for occurrence, set := range sets {
		if len(set) == 0 {
			index, err := newGroup(occurrence)
			if err != nil {
				return indexedStage{}, err
			}
			indices[occurrence] = index
		}
	}
	for i := range child.count() {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		evaluation.bindQueryRow(child.source, child.index(i))
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
			hashes[k] = h
		}
		for occurrence, set := range sets {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			if len(set) == 0 {
				continue
			}
			hash := uint64(14695981039346656037)
			for _, k := range set {
				hash = (hash ^ hashes[k]) * 1099511628211
			}
			index, err := findQueryGroup(ctx, n.grouping, scratch, set, groups, buckets[occurrence][hash])
			if err != nil {
				return indexedStage{}, err
			}
			if index < 0 {
				index, err = newGroup(occurrence)
				if err != nil {
					return indexedStage{}, err
				}
				if buckets[occurrence] == nil {
					buckets[occurrence] = make(map[uint64][]int)
				}
				buckets[occurrence][hash] = append(buckets[occurrence][hash], index)
			}
			indices[occurrence] = index
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
			for _, index := range indices {
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
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
					if call.argument == nil || !value.IsNull() || call.includeNulls {
						groups[index].ordered[p] = append(groups[index].ordered[p], tuple[:call.stageWidth]...)
					}
					continue
				}
				if call.argument != nil && value.IsNull() && !call.includeNulls {
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
	}
	rows := make([][]sqltype.Value, len(groups))
	for i, group := range groups {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		row := group.keys
		// Direct arguments use the final group-key frame, never an input row.
		evaluation.Row = row
		for p, call := range n.calls {
			var value sqltype.Value
			var err error
			if provider, ordered := call.provider.(orderedSetFinalizer); ordered {
				direct := directValues[2 : 2+len(call.direct)]
				for j, expr := range call.direct {
					direct[j], err = expr.Eval(&evaluation)
					if err == nil {
						err = ctx.Err()
					}
					if err != nil {
						return indexedStage{}, err
					}
				}
				var prepared orderedSetPreparation
				prepared, err = provider.PrepareOrdered(ctx, direct, len(group.ordered[p])/call.stageWidth)
				if err == nil {
					if prepared.terminal {
						value = prepared.value
					} else {
						var input orderedAggregateInput
						input, err = sortOrderedAggregate(ctx, state, call, group.ordered[p])
						input.target = directValues[:2+len(call.direct)]
						if err == nil {
							value, err = provider.FinalizeOrdered(ctx, prepared, input, state.input)
						}
					}
				}
			} else {
				if len(call.order) > 0 {
					err = replayOrderedAggregate(ctx, state, call, group.states[p], group.ordered[p])
				}
				if err == nil {
					value, err = call.provider.Finalize(ctx, group.states[p])
				}
			}
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
	source := &querySource{anonymous: rows, materialized: true, flatCount: len(rows), authority: child.source.authority, scope: child.source.scope, locks: child.source.locks}
	if err := child.Close(); err != nil {
		return indexedStage{}, err
	}
	return indexedStage{source: source, schema: n.Schema()}, nil
}

func findQueryGroup(ctx context.Context, grouping []queryGrouping, keys []sqltype.Value, active []int, groups []queryGroup, candidates []int) (int, error) {
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		equal := true
		for _, k := range active {
			same, err := grouping[k].Equal(ctx, keys[k], groups[candidate].keys[k])
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

func sortOrderedAggregate(ctx context.Context, state *queryState, call aggregateCall, tuples []sqltype.Value) (orderedAggregateInput, error) {
	width := call.stageWidth
	indices := make([]int, len(tuples)/width)
	for i := range indices {
		indices[i] = i
	}
	if err := mergeQueryIndices(ctx, indices, func(a, b int) (int, error) {
		return compareOrderedAggregateTuple(ctx, call.order, tuples[a*width:(a+1)*width], tuples[b*width:(b+1)*width], state.input)
	}); err != nil {
		return orderedAggregateInput{}, err
	}
	return orderedAggregateInput{values: tuples, indices: indices, width: width, order: call.order}, ctx.Err()
}

// Sorting, hypothetical placement and peer equality share exactly one typed
// ordering implementation. Values and key metadata are borrowed, never copied.
func compareOrderedAggregateTuple(ctx context.Context, order []sortKey, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error) {
	for _, key := range order {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		a, b := left[key.cache], right[key.cache]
		if a.IsNull() && b.IsNull() {
			continue
		}
		if a.IsNull() || b.IsNull() {
			if a.IsNull() == key.nullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		sign, err := key.comparison.Compare(ctx, a, b, env)
		if err != nil {
			return 0, err
		}
		if err := ctx.Err(); err != nil {
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

func replayOrderedAggregate(ctx context.Context, state *queryState, call aggregateCall, aggregateState any, tuples []sqltype.Value) error {
	input, err := sortOrderedAggregate(ctx, state, call, tuples)
	if err != nil {
		return err
	}
	width := input.width
	for _, index := range input.indices {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := call.provider.Advance(ctx, aggregateState, tuples[index*width], tuples[index*width+1]); err != nil {
			return err
		}
	}
	return ctx.Err()
}
