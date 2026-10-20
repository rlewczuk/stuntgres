package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

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

func (s *aggregateDistinctState) admit(ctx context.Context, grouping []plan.Grouping, tuple []sqltype.Value) (bool, error) {
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

func openAggregateNode(ctx context.Context, state *queryState, n *plan.AggregateNode) (indexedStage, error) {
	if n.PresenceOnly {
		return openQueryNode(ctx, state, n.Child)
	}
	child, err := openQueryNode(ctx, state, n.Child)
	if err != nil {
		return indexedStage{}, err
	}
	child, err = settlePendingQueryMarks(ctx, state, n.Child, child)
	if err != nil {
		return indexedStage{}, err
	}
	defer child.Close()
	if err := child.drain(ctx); err != nil {
		return indexedStage{}, err
	}
	sets := n.Sets
	if sets == nil {
		keys := make([]int, len(n.Keys))
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
	scratch := make([]sqltype.Value, len(n.Keys))
	hashes := make([]uint64, len(n.Keys))
	orderedWidth := 0
	hasDistinct := false
	directWidth := 0
	for _, call := range n.Calls {
		hasDistinct = hasDistinct || len(call.Distinct) > 0
		if len(call.Direct) > directWidth {
			directWidth = len(call.Direct)
		}
		if call.StageWidth > orderedWidth {
			orderedWidth = call.StageWidth
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
	presentation := evaluation.presentation()
	output.Regclass, output.Regtype, output.Regproc = presentation, presentation, presentation
	output.Regrole = presentation
	output.Operator = presentation
	output.Regcollation, output.Regnamespace = presentation, presentation
	output.RegTS = presentation
	newGroup := func(occurrence int) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		group := queryGroup{keys: make([]sqltype.Value, len(n.Schema())), states: make([]any, len(n.Calls))}
		if orderedWidth > 0 {
			group.ordered = make([][]sqltype.Value, len(n.Calls))
		}
		if hasDistinct {
			group.distinct = make([]aggregateDistinctState, len(n.Calls))
		}
		for k, key := range n.Keys {
			group.keys[k] = sqltype.Null(key.ResultUsage().Type)
		}
		for _, k := range sets[occurrence] {
			group.keys[k] = scratch[k]
		}
		if slot := len(n.Keys) + len(n.Calls); slot < len(group.keys) {
			value, err := sqltype.Integer(n.Schema()[slot].Usage.Type, int64(occurrence))
			if err != nil {
				return 0, err
			}
			group.keys[slot] = value
		}
		for i, call := range n.Calls {
			var err error
			if provider, ok := call.Provider.(functions.ContextualAggregateProvider); ok {
				group.states[i], err = provider.InitWithEnvironment(ctx, output)
			} else {
				group.states[i], err = call.Provider.Init(ctx)
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
		for k, key := range n.Keys {
			value, err := key.Eval(&evaluation)
			if err != nil {
				return indexedStage{}, err
			}
			if err = ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			value, err = n.Grouping[k].PrepareKey(ctx, value)
			if err != nil {
				return indexedStage{}, err
			}
			scratch[k] = value
			h, err := n.Grouping[k].Hash(ctx, value)
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
			index, err := findQueryGroup(ctx, n.Grouping, scratch, set, groups, buckets[occurrence][hash])
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
		for p, call := range n.Calls {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			if call.Filter != nil {
				filter, err := call.Filter.Eval(&evaluation)
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
			if call.Argument != nil {
				var err error
				value, err = call.Argument.Eval(&evaluation)
				if err != nil {
					return indexedStage{}, err
				}
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
			}
			var separator sqltype.Value
			if call.Separator != nil {
				separator, err = call.Separator.Eval(&evaluation)
				if err != nil {
					return indexedStage{}, err
				}
				if err := ctx.Err(); err != nil {
					return indexedStage{}, err
				}
			}
			if len(call.Order) > 0 {
				tuple[0], tuple[1] = value, separator
				for _, key := range call.Order {
					if key.Slot < 0 {
						tuple[key.Cache], err = key.Expression.Eval(&evaluation)
						if err != nil {
							return indexedStage{}, err
						}
					}
					if key.Slot < 0 && tuple[key.Cache].Type() != key.Expression.ResultUsage().Type {
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
				if len(call.Distinct) > 0 {
					scratch := [2]sqltype.Value{value, separator}
					admit, err := groups[index].distinct[p].admit(ctx, call.Distinct, scratch[:len(call.Distinct)])
					if err != nil {
						return indexedStage{}, err
					}
					if !admit {
						continue
					}
				}
				if len(call.Order) > 0 {
					if call.Argument == nil || !value.IsNull() || call.IncludeNulls {
						groups[index].ordered[p] = append(groups[index].ordered[p], tuple[:call.StageWidth]...)
					}
					continue
				}
				if call.Argument != nil && value.IsNull() && !call.IncludeNulls {
					continue
				}
				if err := call.Provider.Advance(ctx, groups[index].states[p], value, separator); err != nil {
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
		for p, call := range n.Calls {
			var value sqltype.Value
			var err error
			if provider, ordered := call.Provider.(functions.OrderedAggregate); ordered {
				direct := directValues[2 : 2+len(call.Direct)]
				for j, expr := range call.Direct {
					direct[j], err = expr.Eval(&evaluation)
					if err == nil {
						err = ctx.Err()
					}
					if err != nil {
						return indexedStage{}, err
					}
				}
				var prepared functions.OrderedPreparation
				prepared, err = provider.PrepareOrdered(ctx, direct, len(group.ordered[p])/call.StageWidth)
				if err == nil {
					if prepared.Terminal {
						value = prepared.Value
					} else {
						var input orderedAggregateInput
						input, err = sortOrderedAggregate(ctx, state, call, group.ordered[p])
						if err == nil {
							value, err = provider.FinalizeOrdered(ctx, prepared, input.borrowed(directValues[:2+len(call.Direct)]), state.input)
						}
					}
				}
			} else {
				if len(call.Order) > 0 {
					err = replayOrderedAggregate(ctx, state, call, group.states[p], group.ordered[p])
				}
				if err == nil {
					value, err = call.Provider.Finalize(ctx, group.states[p])
				}
			}
			if err != nil {
				return indexedStage{}, err
			}
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			if value.Type() != call.Provider.ResultUsage().Type {
				return indexedStage{}, fmt.Errorf("stuntgres: invalid finalized aggregate value")
			}
			row[len(n.Keys)+p] = value
		}
		rows[i] = row
	}
	// A nested aggregate borrows the captured input occurrence. Its grouped
	// layout must not replace or close that shared source (or an outer frame).
	source := &querySource{anonymous: rows, materialized: true, flatCount: len(rows), authority: child.source.authority, host: child.source.host, locks: child.source.locks}
	if err := child.Close(); err != nil {
		return indexedStage{}, err
	}
	return indexedStage{source: source, schema: n.Schema()}, nil
}

func findQueryGroup(ctx context.Context, grouping []plan.Grouping, keys []sqltype.Value, active []int, groups []queryGroup, candidates []int) (int, error) {
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

// orderedAggregateInput is the sorted view of one call's staged group input;
// replay and ordered-set finalization borrow it.
type orderedAggregateInput struct {
	values  []sqltype.Value
	indices []int
	width   int
	order   []plan.SortKey
}

// borrowed lends the sorted input to an ordered-set provider, with the
// hypothetical target row and the call's tuple ordering.
func (in orderedAggregateInput) borrowed(target []sqltype.Value) functions.OrderedInput {
	order := in.order
	return functions.OrderedInput{Values: in.values, Indices: in.indices, Width: in.width, Target: target,
		Compare: func(ctx context.Context, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error) {
			return compareOrderedAggregateTuple(ctx, order, left, right, env)
		}}
}

func sortOrderedAggregate(ctx context.Context, state *queryState, call plan.AggregateCall, tuples []sqltype.Value) (orderedAggregateInput, error) {
	width := call.StageWidth
	indices := make([]int, len(tuples)/width)
	for i := range indices {
		indices[i] = i
	}
	if err := mergeQueryIndices(ctx, indices, func(a, b int) (int, error) {
		return compareOrderedAggregateTuple(ctx, call.Order, tuples[a*width:(a+1)*width], tuples[b*width:(b+1)*width], state.input)
	}); err != nil {
		return orderedAggregateInput{}, err
	}
	return orderedAggregateInput{values: tuples, indices: indices, width: width, order: call.Order}, ctx.Err()
}

// Sorting, hypothetical placement and peer equality share exactly one typed
// ordering implementation. Values and key metadata are borrowed, never copied.
func compareOrderedAggregateTuple(ctx context.Context, order []plan.SortKey, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error) {
	for _, key := range order {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		a, b := left[key.Cache], right[key.Cache]
		if a.IsNull() && b.IsNull() {
			continue
		}
		if a.IsNull() || b.IsNull() {
			if a.IsNull() == key.NullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		sign, err := key.Comparison.Compare(ctx, a, b, env)
		if err != nil {
			return 0, err
		}
		if err := ctx.Err(); err != nil {
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

func replayOrderedAggregate(ctx context.Context, state *queryState, call plan.AggregateCall, aggregateState any, tuples []sqltype.Value) error {
	input, err := sortOrderedAggregate(ctx, state, call, tuples)
	if err != nil {
		return err
	}
	width := input.width
	for _, index := range input.indices {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := call.Provider.Advance(ctx, aggregateState, tuples[index*width], tuples[index*width+1]); err != nil {
			return err
		}
	}
	return ctx.Err()
}
