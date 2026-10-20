package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func openDistinctNode(ctx context.Context, state *queryState, n *plan.DistinctNode) (out indexedStage, err error) {
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
	defer func() {
		if closeErr := child.Close(); err == nil && closeErr != nil {
			out, err = indexedStage{}, closeErr
		}
	}()
	width := len(n.Expressions)
	if width == 0 || len(n.Grouping) != width || len(n.Schema()) != width {
		return indexedStage{}, fmt.Errorf("stuntgres: invalid DISTINCT schema")
	}
	source := &querySource{flatWidth: width, materialized: true, authority: state.source.authority, host: state.source.host}
	scratch := make([]sqltype.Value, width)
	buckets := make(map[uint64][]int)
	evaluation := state.evaluation(nil)
	for {
		i, present, failure := child.nextOccurrence(ctx)
		if failure != nil {
			return indexedStage{}, failure
		}
		if !present {
			break
		}
		evaluation.Context = ctx
		evaluation.bindQueryRow(child.source, child.index(i))
		if child.cacheWidth != 0 {
			evaluation.orderValues = child.orderRow(i)
		}
		hash := uint64(14695981039346656037)
		for k, expression := range n.Expressions {
			value, failure := expression.Eval(&evaluation)
			if failure != nil {
				return indexedStage{}, failure
			}
			if failure := validateProducedValue(value, n.Schema()[k].Usage); failure != nil {
				return indexedStage{}, failure
			}
			value, failure = n.Grouping[k].PrepareKey(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			scratch[k] = value
			h, failure := n.Grouping[k].Hash(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		index, failure := findDistinctTuple(ctx, n.Grouping, scratch, source.flat, buckets[hash])
		if failure != nil {
			return indexedStage{}, failure
		}
		if index < 0 {
			buckets[hash] = append(buckets[hash], source.flatCount)
			source.flat = append(source.flat, scratch...)
			source.flatCount++
		}
	}
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	return indexedStage{source: source, schema: n.Schema()}, nil
}

// Hash collisions are candidate lists, never evidence of tuple equality.
func findDistinctTuple[G interface {
	Equal(context.Context, sqltype.Value, sqltype.Value) (bool, error)
}](ctx context.Context, grouping []G, tuple, flat []sqltype.Value, candidates []int) (int, error) {
	for _, index := range candidates {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		row := flat[index*len(grouping) : (index+1)*len(grouping)]
		equal := true
		for i, family := range grouping {
			match, err := family.Equal(ctx, tuple[i], row[i])
			if err != nil {
				return -1, err
			}
			if !match {
				equal = false
				break
			}
		}
		if equal {
			return index, ctx.Err()
		}
	}
	return -1, ctx.Err()
}

func openDistinctOnNode(ctx context.Context, state *queryState, n *plan.DistinctOnNode) (out indexedStage, err error) {
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
	defer func() {
		if err != nil {
			child.Close()
		}
	}()
	width := len(n.Expressions)
	if width == 0 || len(n.Grouping) != width {
		return indexedStage{}, fmt.Errorf("stuntgres: invalid DISTINCT ON keys")
	}
	out = producingStage(&child, n.Schema())
	scratch := make([]sqltype.Value, width)
	var flat []sqltype.Value
	buckets := make(map[uint64][]int)
	evaluation := state.evaluation(nil)
	for {
		i, present, failure := child.nextOccurrence(ctx)
		if failure != nil {
			return indexedStage{}, failure
		}
		if !present {
			break
		}
		evaluation.Context, evaluation.orderValues = ctx, child.orderRow(i)
		evaluation.bindQueryRow(child.source, child.index(i))
		hash := uint64(14695981039346656037)
		for k, e := range n.Expressions {
			value, failure := e.Eval(&evaluation)
			if failure != nil {
				return indexedStage{}, failure
			}
			if failure = validateProducedValue(value, e.ResultUsage()); failure != nil {
				return indexedStage{}, failure
			}
			value, failure = n.Grouping[k].PrepareKey(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			scratch[k] = value
			h, failure := n.Grouping[k].Hash(ctx, value)
			if failure != nil {
				return indexedStage{}, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		found, failure := findDistinctTuple(ctx, n.Grouping, scratch, flat, buckets[hash])
		if failure != nil {
			return indexedStage{}, failure
		}
		if found < 0 {
			buckets[hash] = append(buckets[hash], len(flat)/width)
			flat = append(flat, scratch...)
			retainOccurrence(&out, &child, i)
		}
	}
	if err = ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	return out, nil
}

func (s *queryState) evaluation(ctx context.Context) EvalContext {
	s.inheritCTEs()
	frame := s.frame
	if frame == 0 {
		frame = 1
	}
	evaluation := EvalContext{evalFrame: evalFrame{Scope: 1, Frame: frame, Outer: s.outer, query: s}, evalStatement: evalStatement{Parameters: s.parameters, Input: s.input, Output: s.output}}
	if s.outer != nil {
		evaluation.catalog = s.outer.catalog
	}
	if s.source != nil && s.source.view != nil {
		evaluation.catalog = s.source.view.Catalog()
	}
	if s.source != nil && s.source.routineRecipe != nil {
		recipe := s.source.routineRecipe
		evaluation.routineRecipe = true
		evaluation.routineChecks, evaluation.routineConsumer = s.source.routineChecks, recipe
	}
	evaluation.setContext(ctx)
	return evaluation
}

func queryBoundValue(ctx context.Context, state *queryState, expr expr.Expr, clause string) (int64, bool, error) {
	if expr == nil {
		return 0, false, nil
	}
	evaluation := state.evaluation(ctx)
	value, err := expr.Eval(&evaluation)
	if err != nil {
		return 0, false, err
	}
	if value.IsNull() {
		return 0, false, nil
	}
	n, err := value.Integer()
	if err != nil {
		return 0, false, err
	}
	if n < 0 {
		code := "2201W"
		if clause == "OFFSET" {
			code = "2201X"
		}
		return 0, false, sqlError(code, clause+" must not be negative")
	}
	return n, true, ctx.Err()
}

// Producers append only demanded occurrence identities. Blocking stages drain
// this same seam; the public boundary still publishes only finalized results.
func (s *indexedStage) ensure(ctx context.Context, i int) (bool, error) {
	if s.terminal != nil {
		return false, s.terminal
	}
	if s.closed {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, s.fail(err)
	}
	for s.produce != nil && !s.done && i >= len(s.selection) {
		if err := ctx.Err(); err != nil {
			return false, s.fail(err)
		}
		present, err := s.produce(ctx, s)
		if err != nil {
			return false, s.fail(err)
		}
		if !present {
			s.done = true
		}
	}
	if err := ctx.Err(); err != nil {
		return false, s.fail(err)
	}
	return i < s.count(), nil
}

func (s *indexedStage) nextOccurrence(ctx context.Context) (int, bool, error) {
	present, err := s.ensure(ctx, s.position)
	if err != nil || !present {
		return 0, present, err
	}
	i := s.position
	s.position++
	return i, true, nil
}

func (s *indexedStage) drain(ctx context.Context) error {
	if s.terminal != nil {
		return s.terminal
	}
	if s.closed {
		return nil
	}
	if s.produce == nil {
		if err := ctx.Err(); err != nil {
			return s.fail(err)
		}
		return nil
	}
	for !s.done {
		if _, err := s.ensure(ctx, len(s.selection)); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	return nil
}

func retainOccurrence(out *indexedStage, child *indexedStage, i int) {
	out.source = child.source
	out.selection = append(out.selection, child.index(i))
	out.cacheWidth, out.orderValues = child.cacheWidth, child.orderValues
	if child.cacheWidth != 0 {
		out.cacheOccurrences = append(out.cacheOccurrences, child.orderOccurrence(i))
	}
}

func producingStage(child *indexedStage, schema []catalog.Column) indexedStage {
	return indexedStage{source: child.source, schema: schema, selection: []int{}, cacheWidth: child.cacheWidth, pending: child.pending, finish: child.Close,
		reset: func(ctx context.Context, out *indexedStage) (bool, error) {
			position := child.position
			changed, err := child.rewind(ctx)
			if err != nil {
				return false, err
			}
			if !changed {
				// Cached parent occurrences still own this child's demand frontier.
				child.position = position
				return false, nil
			}
			out.selection, out.cacheOccurrences, out.orderValues = out.selection[:0], out.cacheOccurrences[:0], out.orderValues[:0]
			out.done = false
			return true, nil
		}}
}

func openEvaluationNode(ctx context.Context, state *queryState, n *plan.EvaluationNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.Child)
	if err != nil {
		return indexedStage{}, err
	}
	out := producingStage(&child, n.Schema())
	out.cacheWidth = n.Base + len(n.Expressions)
	evaluation := state.evaluation(nil)
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		i, present, err := child.nextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		start := len(out.orderValues)
		out.orderValues = append(out.orderValues, make([]sqltype.Value, out.cacheWidth)...)
		values := out.orderValues[start:]
		copy(values, child.orderRow(i))
		evaluation.setContext(ctx)
		evaluation.bindQueryRow(child.source, child.index(i))
		evaluation.orderValues = values
		for j, expr := range n.Expressions {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				return false, err
			}
			values[n.Base+j] = value
		}
		out.source = child.source
		out.selection = append(out.selection, child.index(i))
		return true, ctx.Err()
	}
	return out, nil
}

func openLimitNode(ctx context.Context, state *queryState, n *plan.LimitNode) (indexedStage, error) {
	if n.PresenceOnly {
		return openQueryNode(ctx, state, n.Child)
	}
	offset, _, err := queryBoundValue(ctx, state, n.Offset, "OFFSET")
	if err != nil {
		return indexedStage{}, err
	}
	count, finite, err := queryBoundValue(ctx, state, n.Count, "LIMIT")
	if err != nil {
		return indexedStage{}, err
	}
	if finite && count == 0 {
		return indexedStage{source: state.source, schema: n.Schema(), selection: []int{}}, nil
	}
	child, err := openQueryNode(ctx, state, n.Child)
	if err != nil {
		return indexedStage{}, err
	}
	// A bounded view cannot defer inherited rowmarks past its own survivor
	// count. Plain scalar views still defer until enclosing qualification.
	if state.inherited.Mode != 0 && state.source.locks != nil {
		occurrences := queryTreeOccurrences(n.Child, state.source, nil)
		if len(occurrences) != 0 {
			child.pending = append(child.pending, queryPendingMark{occurrences: occurrences, mark: state.inherited})
		}
	}
	child, err = settlePendingQueryMarks(ctx, state, n.Child, child)
	if err != nil {
		return indexedStage{}, err
	}
	if offset == 0 && !finite {
		return child, nil
	}
	out := producingStage(&child, n.Schema())
	initialOffset, initialCount := offset, count
	baseReset := out.reset
	out.reset = func(ctx context.Context, out *indexedStage) (bool, error) {
		changed, err := baseReset(ctx, out)
		if err != nil || !changed {
			return changed, err
		}
		offset, count = initialOffset, initialCount
		return true, nil
	}
	var boundary []sqltype.Value
	if finite && n.WithTies {
		boundary = make([]sqltype.Value, len(n.PeerKeys))
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		if finite && count == 0 && !n.WithTies {
			if state.retain {
				return false, nil
			}
			return false, child.Close()
		}
		for {
			i, present, err := child.nextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			if offset > 0 {
				offset--
				continue
			}
			if finite && count == 0 {
				peer, err := queryTiesPeer(ctx, &child, i, n.PeerKeys, boundary, state.input)
				if err != nil {
					return false, err
				}
				if !peer {
					if state.retain {
						return false, nil
					}
					return false, child.Close()
				}
			}
			retainOccurrence(out, &child, i)
			if finite && count > 0 {
				count--
				if count == 0 && n.WithTies {
					captureQueryTiesBoundary(&child, i, n.PeerKeys, boundary)
				}
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}

func validateProducedValue(value sqltype.Value, usage sqltype.Usage) error {
	if !value.ValidResult(usage) {
		return fmt.Errorf("stuntgres: invalid produced query value")
	}
	return nil
}

// Exclusions leave at most three ordered intervals. Count admitted rows, not
// nonnull values, without allocating or walking the whole frame for each result.
func windowValueMember(mode parser.FrameExclusion, head, tail, current, peerStart, peerEnd int, ordinal int64, reverse bool) (int, bool) {
	if head >= tail || ordinal < 0 {
		return 0, false
	}
	intervals := [3][2]int{{head, tail}}
	count := 1
	switch mode {
	case parser.ExcludeCurrentRow:
		intervals = [3][2]int{{head, min(tail, current)}, {max(head, current+1), tail}}
		count = 2
	case parser.ExcludeGroup:
		intervals = [3][2]int{{head, min(tail, peerStart)}, {max(head, peerEnd), tail}}
		count = 2
	case parser.ExcludeTies:
		intervals = [3][2]int{{head, min(tail, peerStart)}, {max(head, current), min(tail, current+1)}, {max(head, peerEnd), tail}}
		count = 3
	}
	for step := range count {
		index := step
		if reverse {
			index = count - 1 - step
		}
		start, end := intervals[index][0], intervals[index][1]
		length := int64(max(0, end-start))
		if ordinal < length {
			if reverse {
				return end - 1 - int(ordinal), true
			}
			return start + int(ordinal), true
		}
		ordinal -= length
	}
	return 0, false
}

func evaluateWindowValue(ctx context.Context, evaluation *EvalContext, child *indexedStage, call plan.WindowCall, rows []int, frame plan.WindowFrame, head, tail, current, peerStart, peerEnd int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	usage := call.Expression.ResultUsage()
	ordinal := int64(0)
	if call.Operation == plan.WindowNth {
		evaluation.bindQueryRow(child.source, child.index(rows[current]))
		value, err := call.Ordinal.Eval(evaluation)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := validateProducedValue(value, call.Ordinal.ResultUsage()); err != nil {
			return sqltype.Value{}, err
		}
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(usage.Type), nil
		}
		ordinal, err = value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if ordinal <= 0 {
			return sqltype.Value{}, sqlError("22016", "argument of nth_value must be greater than zero")
		}
		ordinal--
	}
	member, present := windowValueMember(frame.Exclusion, head, tail, current, peerStart, peerEnd, ordinal, call.Operation == plan.WindowLast)
	if !present {
		return sqltype.Null(usage.Type), nil
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	evaluation.bindQueryRow(child.source, child.index(rows[member]))
	value, err := call.Argument.Eval(evaluation)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := validateProducedValue(value, usage); err != nil {
		return sqltype.Value{}, err
	}
	// Value owns immutable payloads; retaining it in the output overlay neither
	// aliases a reusable scan buffer nor reevaluates/caches the selected member.
	return value, ctx.Err()
}
