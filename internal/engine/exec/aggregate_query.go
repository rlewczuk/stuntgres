package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

type QueryGroup struct {
	occurrence int // Grouping-set occurrence, including duplicate sets.
	Keys       []sqltype.Value
	states     []any
	ordered    [][]sqltype.Value
	distinct   []AggregateDistinctState
}

type AggregateDistinctState struct {
	Buckets map[uint64][]int
	Tuples  []sqltype.Value
}

func (s *AggregateDistinctState) Admit(ctx context.Context, grouping []plan.Grouping, tuple []sqltype.Value, evaluation expr.Context) (bool, error) {
	hash := uint64(14695981039346656037)
	for i, family := range grouping {
		if err := sampleCheckpoint(ctx); err != nil {
			return false, err
		}
		value, err := family.PrepareKey(ctx, tuple[i])
		if err != nil {
			return false, err
		}
		if err := sampleCheckpoint(ctx); err != nil {
			return false, err
		}
		tuple[i] = value
		h, err := family.HashWithEvaluation(ctx, value, evaluation)
		if err != nil {
			return false, err
		}
		if err := sampleCheckpoint(ctx); err != nil {
			return false, err
		}
		hash = (hash ^ h) * 1099511628211
	}
	index, err := FindDistinctTuple(ctx, grouping, tuple, s.Tuples, s.Buckets[hash], evaluation)
	if err != nil || index >= 0 {
		return false, err
	}
	if s.Buckets == nil {
		s.Buckets = make(map[uint64][]int)
	}
	s.Buckets[hash] = append(s.Buckets[hash], len(s.Tuples)/len(grouping))
	s.Tuples = append(s.Tuples, tuple...)
	return true, sampleCheckpoint(ctx)
}

func openAggregateNode(ctx context.Context, state *State, n *plan.AggregateNode) (out Stage, err error) {
	if n.PresenceOnly {
		return OpenNode(ctx, state, n.Child)
	}
	if err := util.ExecutionCheckpoint(ctx, "query.aggregate"); err != nil {
		return Stage{}, err
	}
	evaluation := state.Evaluation(ctx)
	for _, call := range n.Calls {
		if err := sampleCheckpoint(ctx); err != nil {
			return Stage{}, err
		}
		if state.aggregatePartials == nil {
			// Admit one aggregate support/input initialization, before the child
			// is materialized. This is not admission for ordered-input sorting.
			if err := workadmission.Admit(ctx, workadmission.WorkAggregateInput); err != nil {
				return Stage{}, err
			}
			if err := AdmitAggregateSupport(&evaluation, &call); err != nil {
				return Stage{}, err
			}
			if err := sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
		}
		if provider, ok := call.Provider.(*TransitionAggregate); ok && state.aggregatePartials == nil {
			if err := provider.Start(&evaluation); err != nil {
				return Stage{}, err
			}
		}
		if err := sampleCheckpoint(ctx); err != nil {
			return Stage{}, err
		}
	}
	child, err := OpenNode(ctx, state, n.Child)
	if err != nil {
		return Stage{}, err
	}
	child, err = SettlePendingQueryMarks(ctx, state, n.Child, child)
	if err != nil {
		return Stage{}, err
	}
	defer func() {
		closeErr := child.Close()
		_, exhausted := err.(*sampleBudget)
		if closeErr != nil && (err == nil || exhausted) {
			out, err = Stage{}, closeErr
		}
	}()
	if err := child.Drain(ctx); err != nil {
		return Stage{}, err
	}
	sets := n.Sets
	if sets == nil {
		keys := make([]int, len(n.Keys))
		for i := range keys {
			if err := sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
			keys[i] = i
		}
		sets = [][]int{keys}
	}
	groups := make([]QueryGroup, 0)
	var buckets []map[uint64][]int
	if !state.Methods.NoHashAgg {
		buckets = make([]map[uint64][]int, len(sets))
	}
	indices := make([]int, len(sets))
	for i := range indices {
		if err := sampleCheckpoint(ctx); err != nil {
			return Stage{}, err
		}
		indices[i] = -1
	}
	scratch := make([]sqltype.Value, len(n.Keys))
	var hashes []uint64
	if !state.Methods.NoHashAgg {
		hashes = make([]uint64, len(n.Keys))
	}
	orderedWidth := 0
	hasDistinct := false
	directWidth := 0
	argumentWidth := 0
	for _, call := range n.Calls {
		if err := sampleCheckpoint(ctx); err != nil {
			return Stage{}, err
		}
		hasDistinct = hasDistinct || len(call.Distinct) > 0
		argumentWidth = max(argumentWidth, len(call.Arguments))
		if len(call.Direct) > directWidth {
			directWidth = len(call.Direct)
		}
		if !ownedOrderedAggregate(call.Provider) && call.StageWidth > orderedWidth {
			orderedWidth = call.StageWidth
		}
	}
	tuple := make([]sqltype.Value, orderedWidth)
	arguments := make([]sqltype.Value, argumentWidth)
	distinctTuple := make([]sqltype.Value, argumentWidth)
	// Reserved ordinary payload slots keep the hypothetical target in the
	// same layout as retained input tuples. Reused only after finalization.
	var directLocal [2]sqltype.Value
	directValues := directLocal[:]
	if directWidth > 0 {
		directValues = make([]sqltype.Value, 2+directWidth)
	}
	output := evaluation.Output
	presentation := evaluation.Presentation()
	output.Regclass, output.Regtype, output.Regproc = presentation, presentation, presentation
	output.Regrole = presentation
	output.Operator = presentation
	output.Regcollation, output.Regnamespace = presentation, presentation
	output.RegTS = presentation
	newGroup := func(occurrence int) (int, error) {
		if err := sampleCheckpoint(ctx); err != nil {
			return 0, err
		}
		group := QueryGroup{occurrence: occurrence, Keys: make([]sqltype.Value, len(n.Schema())), states: make([]any, len(n.Calls))}
		if orderedWidth > 0 {
			group.ordered = make([][]sqltype.Value, len(n.Calls))
		}
		if hasDistinct {
			group.distinct = make([]AggregateDistinctState, len(n.Calls))
		}
		for k, key := range n.Keys {
			if err := sampleCheckpoint(ctx); err != nil {
				return 0, err
			}
			group.Keys[k] = sqltype.Null(key.ResultUsage().Type)
		}
		for _, k := range sets[occurrence] {
			if err := sampleCheckpoint(ctx); err != nil {
				return 0, err
			}
			group.Keys[k] = scratch[k]
		}
		if slot := len(n.Keys) + len(n.Calls); slot < len(group.Keys) {
			value, err := sqltype.Integer(n.Schema()[slot].Usage.Type, int64(occurrence))
			if err != nil {
				return 0, err
			}
			group.Keys[slot] = value
		}
		for i, dependent := range n.Dependent {
			value, err := dependent.Eval(&evaluation)
			if err != nil {
				return 0, err
			}
			if err := sampleCheckpoint(ctx); err != nil {
				return 0, err
			}
			if value.Type() != dependent.ResultUsage().Type {
				return 0, fmt.Errorf("stuntgres: invalid dependent group value")
			}
			group.Keys[n.DependentStart()+i] = value
		}
		for i, call := range n.Calls {
			if err := sampleCheckpoint(ctx); err != nil {
				return 0, err
			}
			var err error
			if provider, ok := call.Provider.(functions.ExecutionAggregateProvider); ok {
				group.states[i], err = provider.InitWithContext(ctx, &evaluation)
			} else if provider, ok := call.Provider.(functions.ContextualAggregateProvider); ok {
				group.states[i], err = provider.InitWithEnvironment(ctx, output)
			} else {
				group.states[i], err = call.Provider.Init(ctx)
			}
			if err != nil {
				return 0, err
			}
			if err = sampleCheckpoint(ctx); err != nil {
				return 0, err
			}
		}
		groups = append(groups, group)
		return len(groups) - 1, nil
	}
	for occurrence, set := range sets {
		if err := sampleCheckpoint(ctx); err != nil {
			return Stage{}, err
		}
		if len(set) == 0 {
			index, err := newGroup(occurrence)
			if err != nil {
				return Stage{}, err
			}
			indices[occurrence] = index
		}
	}
	for i := range child.Count() {
		if err := sampleCheckpoint(ctx); err != nil {
			return Stage{}, err
		}
		evaluation.BindQueryRow(child.Source, child.Index(i))
		for k, key := range n.Keys {
			value, err := key.Eval(&evaluation)
			if err != nil {
				return Stage{}, err
			}
			if err = sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
			value, err = n.Grouping[k].PrepareKey(ctx, value)
			if err != nil {
				return Stage{}, err
			}
			if err := sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
			scratch[k] = value
			if !state.Methods.NoHashAgg {
				h, err := n.Grouping[k].HashWithEvaluation(ctx, value, &evaluation)
				if err != nil {
					return Stage{}, err
				}
				if err := sampleCheckpoint(ctx); err != nil {
					return Stage{}, err
				}
				hashes[k] = h
			}
		}
		for occurrence, set := range sets {
			if err := sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
			if len(set) == 0 {
				continue
			}
			hash := uint64(14695981039346656037)
			index := -1
			var err error
			if state.Methods.NoHashAgg {
				index, err = findEqualityQueryGroup(ctx, n.Grouping, scratch, set, groups, occurrence, &evaluation)
			} else {
				for _, k := range set {
					if err := sampleCheckpoint(ctx); err != nil {
						return Stage{}, err
					}
					hash = (hash ^ hashes[k]) * 1099511628211
				}
				index, err = FindQueryGroup(ctx, n.Grouping, scratch, set, groups, buckets[occurrence][hash], &evaluation)
			}
			if err != nil {
				return Stage{}, err
			}
			if index < 0 {
				index, err = newGroup(occurrence)
				if err != nil {
					return Stage{}, err
				}
				if !state.Methods.NoHashAgg {
					if buckets[occurrence] == nil {
						buckets[occurrence] = make(map[uint64][]int)
					}
					buckets[occurrence][hash] = append(buckets[occurrence][hash], index)
				}
			}
			indices[occurrence] = index
		}
		for p, call := range n.Calls {
			if err := sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
			if call.Filter != nil {
				filter, err := call.Filter.Eval(&evaluation)
				if err != nil {
					return Stage{}, err
				}
				if err := sampleCheckpoint(ctx); err != nil {
					return Stage{}, err
				}
				if filter.IsNull() {
					continue
				}
				admit, err := filter.Boolean()
				if err != nil {
					return Stage{}, err
				}
				if !admit {
					continue
				}
			}
			values := arguments[:len(call.Arguments)]
			for j, argument := range call.Arguments {
				values[j], err = argument.Eval(&evaluation)
				if err != nil {
					return Stage{}, err
				}
				if err = sampleCheckpoint(ctx); err != nil {
					return Stage{}, err
				}
			}
			bufferOrdered := len(call.Order) > 0 && !ownedOrderedAggregate(call.Provider)
			if bufferOrdered {
				copy(tuple, values)
				for _, key := range call.Order {
					if key.Slot < 0 {
						tuple[key.Cache], err = key.Expression.Eval(&evaluation)
						if err != nil {
							return Stage{}, err
						}
					}
					if key.Slot < 0 && tuple[key.Cache].Type() != key.Expression.ResultUsage().Type {
						return Stage{}, fmt.Errorf("stuntgres: invalid aggregate ordering value")
					}
					if err := sampleCheckpoint(ctx); err != nil {
						return Stage{}, err
					}
				}
			}
			for _, index := range indices {
				if err := sampleCheckpoint(ctx); err != nil {
					return Stage{}, err
				}
				if len(call.Distinct) > 0 {
					copy(distinctTuple, values)
					admit, err := groups[index].distinct[p].Admit(ctx, call.Distinct, distinctTuple[:len(call.Distinct)], &evaluation)
					if err != nil {
						return Stage{}, err
					}
					if !admit {
						continue
					}
				}
				if bufferOrdered {
					if len(values) == 0 || !values[0].IsNull() || call.IncludeNulls {
						groups[index].ordered[p] = append(groups[index].ordered[p], tuple[:call.StageWidth]...)
					}
					continue
				}
				if len(values) > 0 && values[0].IsNull() && !call.IncludeNulls {
					continue
				}
				if err := call.Provider.Advance(ctx, groups[index].states[p], values, &evaluation); err != nil {
					return Stage{}, err
				}
				if err := sampleCheckpoint(ctx); err != nil {
					return Stage{}, err
				}
			}
		}
	}
	if state.aggregatePartials != nil {
		*state.aggregatePartials = groups
		return Stage{}, sampleCheckpoint(ctx)
	}
	rows := make([][]sqltype.Value, len(groups))
	for i, group := range groups {
		if err := sampleCheckpoint(ctx); err != nil {
			return Stage{}, err
		}
		row := group.Keys
		// Direct arguments use the final group-key frame, never an input row.
		evaluation.Row = row
		for p, call := range n.Calls {
			if err := sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
			var value sqltype.Value
			var err error
			direct := directValues[2 : 2+len(call.Direct)]
			for j, expression := range call.Direct {
				direct[j], err = expression.Eval(&evaluation)
				if err == nil {
					err = sampleCheckpoint(ctx)
				}
				if err != nil {
					return Stage{}, err
				}
			}
			if provider, ordered := call.Provider.(functions.OrderedAggregate); ordered {
				var prepared functions.OrderedPreparation
				prepared, err = provider.PrepareOrdered(ctx, direct, len(group.ordered[p])/call.StageWidth)
				if err == nil {
					err = sampleCheckpoint(ctx)
				}
				if err == nil {
					if prepared.Terminal {
						value = prepared.Value
					} else {
						var input OrderedAggregateInput
						input, err = sortOrderedAggregate(ctx, state, call, group.ordered[p])
						if err == nil {
							value, err = provider.FinalizeOrdered(ctx, prepared, input.Borrowed(directValues[:2+len(call.Direct)]), state.Input)
						}
					}
				}
			} else {
				if len(call.Order) > 0 && !ownedOrderedAggregate(call.Provider) {
					err = replayOrderedAggregate(ctx, state, call, group.states[p], group.ordered[p])
				}
				if err == nil {
					if contextual, ok := call.Provider.(functions.ExecutionAggregateProvider); ok {
						value, err = contextual.FinalizeWithContext(ctx, group.states[p], direct, &evaluation)
					} else {
						value, err = call.Provider.Finalize(ctx, group.states[p])
					}
				}
			}
			if err != nil {
				return Stage{}, err
			}
			if err := sampleCheckpoint(ctx); err != nil {
				return Stage{}, err
			}
			if value.Type() != call.Provider.ResultUsage().Type {
				return Stage{}, fmt.Errorf("stuntgres: invalid finalized aggregate value")
			}
			row[len(n.Keys)+p] = value
		}
		rows[i] = row
	}
	// A nested aggregate borrows the captured input occurrence. Its grouped
	// layout must not replace or close that shared source (or an outer frame).
	source := &Source{Anonymous: rows, Materialized: true, FlatCount: len(rows), Authority: child.Source.Authority, Host: child.Source.Host, Locks: child.Source.Locks}
	if err := child.Close(); err != nil {
		return Stage{}, err
	}
	return Stage{Source: source, Columns: n.Schema()}, nil
}

func FindQueryGroup(ctx context.Context, grouping []plan.Grouping, keys []sqltype.Value, active []int, groups []QueryGroup, candidates []int, evaluation expr.Context) (int, error) {
	for _, candidate := range candidates {
		if err := sampleCheckpoint(ctx); err != nil {
			return -1, err
		}
		equal := true
		for _, k := range active {
			same, err := grouping[k].EqualWithEvaluation(ctx, keys[k], groups[candidate].Keys[k], evaluation)
			if err != nil {
				return -1, err
			}
			if err := sampleCheckpoint(ctx); err != nil {
				return -1, err
			}
			if !same {
				equal = false
				break
			}
		}
		if err := sampleCheckpoint(ctx); err != nil {
			return -1, err
		}
		if equal {
			return candidate, nil
		}
	}
	return -1, sampleCheckpoint(ctx)
}

func findEqualityQueryGroup(ctx context.Context, grouping []plan.Grouping, keys []sqltype.Value, active []int, groups []QueryGroup, occurrence int, evaluation expr.Context) (int, error) {
	for candidate := range groups {
		if err := sampleCheckpoint(ctx); err != nil {
			return -1, err
		}
		if groups[candidate].occurrence != occurrence {
			continue
		}
		one := [1]int{candidate}
		index, err := FindQueryGroup(ctx, grouping, keys, active, groups, one[:], evaluation)
		if err != nil || index >= 0 {
			return index, err
		}
	}
	return -1, sampleCheckpoint(ctx)
}

// OrderedAggregateInput is the sorted view of one call's staged group input;
// replay and ordered-set finalization borrow it.
type OrderedAggregateInput struct {
	Values  []sqltype.Value
	Indices []int
	Width   int
	Order   []plan.SortKey
}

// Borrowed lends the sorted input to an ordered-set provider, with the
// hypothetical target row and the call's tuple ordering.
func (in OrderedAggregateInput) Borrowed(target []sqltype.Value) functions.OrderedInput {
	order := in.Order
	return functions.OrderedInput{Values: in.Values, Indices: in.Indices, Width: in.Width, Target: target,
		Compare: func(ctx context.Context, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error) {
			return CompareOrderedAggregateTuple(ctx, order, left, right, env)
		}}
}

func sortOrderedAggregate(ctx context.Context, state *State, call plan.AggregateCall, tuples []sqltype.Value) (OrderedAggregateInput, error) {
	width := call.StageWidth
	indices := make([]int, len(tuples)/width)
	for i := range indices {
		if err := sampleCheckpoint(ctx); err != nil {
			return OrderedAggregateInput{}, err
		}
		indices[i] = i
	}
	if err := MergeQueryIndices(ctx, indices, func(a, b int) (int, error) {
		sign, err := CompareOrderedAggregateTuple(ctx, call.Order, tuples[a*width:(a+1)*width], tuples[b*width:(b+1)*width], state.Input)
		if err != nil {
			return sign, err
		}
		if err := util.ExecutionCheckpoint(ctx, "query.aggregate.sort"); err != nil {
			return sign, err
		}
		return sign, nil
	}); err != nil {
		return OrderedAggregateInput{}, err
	}
	return OrderedAggregateInput{Values: tuples, Indices: indices, Width: width, Order: call.Order}, sampleCheckpoint(ctx)
}

// Sorting, hypothetical placement and peer equality share exactly one typed
// ordering implementation. Values and key metadata are borrowed, never copied.
func CompareOrderedAggregateTuple(ctx context.Context, order []plan.SortKey, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error) {
	for _, key := range order {
		if err := sampleCheckpoint(ctx); err != nil {
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
		if err := sampleCheckpoint(ctx); err != nil {
			return 0, err
		}
		if sign != 0 {
			if key.Desc {
				sign = -sign
			}
			return sign, nil
		}
	}
	return 0, sampleCheckpoint(ctx)
}

func replayOrderedAggregate(ctx context.Context, state *State, call plan.AggregateCall, aggregateState any, tuples []sqltype.Value) error {
	input, err := sortOrderedAggregate(ctx, state, call, tuples)
	if err != nil {
		return err
	}
	width := input.Width
	evaluation := state.Evaluation(ctx)
	for _, index := range input.Indices {
		if err := sampleCheckpoint(ctx); err != nil {
			return err
		}
		if err := call.Provider.Advance(ctx, aggregateState, tuples[index*width:index*width+len(call.Arguments)], &evaluation); err != nil {
			return err
		}
	}
	return sampleCheckpoint(ctx)
}

func ownedOrderedAggregate(provider functions.AggregateProvider) bool {
	p, ok := provider.(*TransitionAggregate)
	return ok && p.Aggregate.Aggregate.Definition.Kind != "n"
}
