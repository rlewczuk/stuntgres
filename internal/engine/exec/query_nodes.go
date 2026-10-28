package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func openDistinctNode(ctx context.Context, state *State, n *plan.DistinctNode) (out Stage, err error) {
	if n.PresenceOnly {
		return OpenNode(ctx, state, n.Child)
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
		if closeErr := child.Close(); err == nil && closeErr != nil {
			out, err = Stage{}, closeErr
		}
	}()
	width := len(n.Expressions)
	if width == 0 || len(n.Grouping) != width || len(n.Schema()) != width {
		return Stage{}, fmt.Errorf("stuntgres: invalid DISTINCT schema")
	}
	source := &Source{FlatWidth: width, Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host}
	scratch := make([]sqltype.Value, width)
	var buckets map[uint64][]int
	if !state.Methods.NoHashAgg {
		buckets = make(map[uint64][]int)
	}
	evaluation := state.Evaluation(nil)
	for {
		i, present, failure := child.NextOccurrence(ctx)
		if failure != nil {
			return Stage{}, failure
		}
		if !present {
			break
		}
		evaluation.Context = ctx
		evaluation.BindQueryRow(child.Source, child.Index(i))
		if child.CacheWidth != 0 {
			evaluation.OrderValues = child.OrderRow(i)
		}
		hash := uint64(14695981039346656037)
		for k, expression := range n.Expressions {
			value, failure := expression.Eval(&evaluation)
			if failure != nil {
				return Stage{}, failure
			}
			if failure := ValidateProducedValue(value, n.Schema()[k].Usage); failure != nil {
				return Stage{}, failure
			}
			value, failure = n.Grouping[k].PrepareKey(ctx, value)
			if failure != nil {
				return Stage{}, failure
			}
			scratch[k] = value
			if !state.Methods.NoHashAgg {
				h, failure := n.Grouping[k].Hash(ctx, value)
				if failure != nil {
					return Stage{}, failure
				}
				hash = (hash ^ h) * 1099511628211
			}
		}
		var index int
		if state.Methods.NoHashAgg {
			index, failure = findEqualityDistinctTuple(ctx, n.Grouping, scratch, source.Flat)
		} else {
			index, failure = FindDistinctTuple(ctx, n.Grouping, scratch, source.Flat, buckets[hash])
		}
		if failure != nil {
			return Stage{}, failure
		}
		if index < 0 {
			if !state.Methods.NoHashAgg {
				buckets[hash] = append(buckets[hash], source.FlatCount)
			}
			source.Flat = append(source.Flat, scratch...)
			source.FlatCount++
		}
	}
	if err := ctx.Err(); err != nil {
		return Stage{}, err
	}
	return Stage{Source: source, Columns: n.Schema()}, nil
}

// Hash collisions are candidate lists, never evidence of tuple equality.
func FindDistinctTuple[G interface {
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

func findEqualityDistinctTuple(ctx context.Context, grouping []plan.Grouping, tuple, flat []sqltype.Value) (int, error) {
	for candidate := range len(flat) / len(grouping) {
		one := [1]int{candidate}
		index, err := FindDistinctTuple(ctx, grouping, tuple, flat, one[:])
		if err != nil || index >= 0 {
			return index, err
		}
	}
	return -1, ctx.Err()
}

func openDistinctOnNode(ctx context.Context, state *State, n *plan.DistinctOnNode) (out Stage, err error) {
	if n.PresenceOnly {
		return OpenNode(ctx, state, n.Child)
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
		if err != nil {
			child.Close()
		}
	}()
	width := len(n.Expressions)
	if width == 0 || len(n.Grouping) != width {
		return Stage{}, fmt.Errorf("stuntgres: invalid DISTINCT ON keys")
	}
	out = ProducingStage(&child, n.Schema())
	scratch := make([]sqltype.Value, width)
	var flat []sqltype.Value
	buckets := make(map[uint64][]int)
	evaluation := state.Evaluation(nil)
	for {
		i, present, failure := child.NextOccurrence(ctx)
		if failure != nil {
			return Stage{}, failure
		}
		if !present {
			break
		}
		evaluation.Context, evaluation.OrderValues = ctx, child.OrderRow(i)
		evaluation.BindQueryRow(child.Source, child.Index(i))
		hash := uint64(14695981039346656037)
		for k, e := range n.Expressions {
			value, failure := e.Eval(&evaluation)
			if failure != nil {
				return Stage{}, failure
			}
			if failure = ValidateProducedValue(value, e.ResultUsage()); failure != nil {
				return Stage{}, failure
			}
			value, failure = n.Grouping[k].PrepareKey(ctx, value)
			if failure != nil {
				return Stage{}, failure
			}
			scratch[k] = value
			h, failure := n.Grouping[k].Hash(ctx, value)
			if failure != nil {
				return Stage{}, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		found, failure := FindDistinctTuple(ctx, n.Grouping, scratch, flat, buckets[hash])
		if failure != nil {
			return Stage{}, failure
		}
		if found < 0 {
			buckets[hash] = append(buckets[hash], len(flat)/width)
			flat = append(flat, scratch...)
			RetainOccurrence(&out, &child, i)
		}
	}
	if err = ctx.Err(); err != nil {
		return Stage{}, err
	}
	return out, nil
}

func (s *State) Evaluation(ctx context.Context) Context {
	s.CapturePlannerAdmission()
	s.inheritCTEs()
	frame := s.Frame
	if frame == 0 {
		frame = 1
	}
	evaluation := Context{EvalFrame: EvalFrame{Scope: 1, Frame: frame, Outer: s.Outer, Query: s}, EvalStatement: EvalStatement{Parameters: s.Parameters, Input: s.Input, Output: s.Output}}
	if s.Outer != nil {
		evaluation.Catalog = s.Outer.Catalog
	}
	if s.Source != nil && s.Source.View != nil {
		evaluation.Catalog = s.Source.View.Catalog()
	}
	if s.Source != nil && (s.Source.RoutineRecipe != nil || s.Source.RoutineChecks != nil) {
		evaluation.RoutineRecipe = true
		evaluation.RoutineChecks, evaluation.RoutineConsumer = s.Source.RoutineChecks, s.Source.RoutineRecipe
	}
	evaluation.setContext(ctx)
	if a := s.aggregateAdmission; a != nil {
		evaluation.RoutineAuthority = a
		evaluation.RoutineRecipe, evaluation.RoutineChecks = true, a.dependencies
	}
	return evaluation
}

func queryBoundValue(ctx context.Context, state *State, expr expr.Expr, clause string) (int64, bool, error) {
	if expr == nil {
		return 0, false, nil
	}
	evaluation := state.Evaluation(ctx)
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
func (s *Stage) ensure(ctx context.Context, i int) (bool, error) {
	if s.Terminal != nil {
		return false, s.Terminal
	}
	if s.Closed {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, s.fail(err)
	}
	for s.Produce != nil && !s.Done && i >= s.Count() {
		if err := ctx.Err(); err != nil {
			return false, s.fail(err)
		}
		present, err := s.Produce(ctx, s)
		if err != nil {
			return false, s.fail(err)
		}
		if !present {
			s.Done = true
		}
	}
	if err := ctx.Err(); err != nil {
		return false, s.fail(err)
	}
	return i < s.Count(), nil
}

func (s *Stage) NextOccurrence(ctx context.Context) (int, bool, error) {
	present, err := s.ensure(ctx, s.Position)
	if err != nil || !present {
		return 0, present, err
	}
	i := s.Position
	s.Position++
	return i, true, nil
}

func (s *Stage) Drain(ctx context.Context) error {
	if s.Terminal != nil {
		return s.Terminal
	}
	if s.Closed {
		return nil
	}
	if s.Produce == nil {
		if err := ctx.Err(); err != nil {
			return s.fail(err)
		}
		return nil
	}
	for !s.Done {
		if _, err := s.ensure(ctx, s.Count()); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	return nil
}

func RetainOccurrence(out *Stage, child *Stage, i int) {
	out.Source = child.Source
	out.Selection = append(out.Selection, child.Index(i))
	out.CacheWidth, out.OrderValues = child.CacheWidth, child.OrderValues
	if child.CacheWidth != 0 {
		out.CacheOccurrences = append(out.CacheOccurrences, child.orderOccurrence(i))
	}
}

func ProducingStage(child *Stage, schema []catalog.Column) Stage {
	return Stage{Source: child.Source, Columns: schema, Selection: []int{}, CacheWidth: child.CacheWidth, Pending: child.Pending, Finish: child.Close,
		Reset: func(ctx context.Context, out *Stage) (bool, error) {
			position := child.Position
			changed, err := child.Rewind(ctx)
			if err != nil {
				return false, err
			}
			if !changed {
				// Cached parent occurrences still own this child's demand frontier.
				child.Position = position
				return false, nil
			}
			out.Selection, out.CacheOccurrences, out.OrderValues = out.Selection[:0], out.CacheOccurrences[:0], out.OrderValues[:0]
			out.Done = false
			return true, nil
		}}
}

func OpenEvaluationNode(ctx context.Context, state *State, n *plan.EvaluationNode) (Stage, error) {
	child, err := OpenNode(ctx, state, n.Child)
	if err != nil {
		return Stage{}, err
	}
	out := ProducingStage(&child, n.Schema())
	out.CacheWidth = n.Base + len(n.Expressions)
	evaluation := state.Evaluation(nil)
	evaluation.Context = nil
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		defer func() { evaluation.Context = nil }()
		i, present, err := child.NextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		start := len(out.OrderValues)
		out.OrderValues = append(out.OrderValues, make([]sqltype.Value, out.CacheWidth)...)
		values := out.OrderValues[start:]
		copy(values, child.OrderRow(i))
		evaluation.setContext(ctx)
		evaluation.BindQueryRow(child.Source, child.Index(i))
		evaluation.OrderValues = values
		for j, expr := range n.Expressions {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				return false, err
			}
			values[n.Base+j] = value
		}
		out.Source = child.Source
		out.Selection = append(out.Selection, child.Index(i))
		return true, ctx.Err()
	}
	return out, nil
}

func OpenLimitNode(ctx context.Context, state *State, n *plan.LimitNode) (Stage, error) {
	if n.PresenceOnly {
		return OpenNode(ctx, state, n.Child)
	}
	offset, _, err := queryBoundValue(ctx, state, n.Offset, "OFFSET")
	if err != nil {
		return Stage{}, err
	}
	count, finite, err := queryBoundValue(ctx, state, n.Count, "LIMIT")
	if err != nil {
		return Stage{}, err
	}
	if finite && count == 0 {
		return Stage{Source: state.Source, Columns: n.Schema(), Selection: []int{}}, nil
	}
	child, err := OpenNode(ctx, state, n.Child)
	if err != nil {
		return Stage{}, err
	}
	// A bounded view cannot defer inherited rowmarks past its own survivor
	// count. Plain scalar views still defer until enclosing qualification.
	if state.inherited.Mode != 0 && state.Source.Locks != nil {
		occurrences := QueryTreeOccurrences(n.Child, state.Source, nil)
		if len(occurrences) != 0 {
			child.Pending = append(child.Pending, queryPendingMark{Occurrences: occurrences, Mark: state.inherited})
		}
	}
	child, err = SettlePendingQueryMarks(ctx, state, n.Child, child)
	if err != nil {
		return Stage{}, err
	}
	if offset == 0 && !finite {
		return child, nil
	}
	out := ProducingStage(&child, n.Schema())
	initialOffset, initialCount := offset, count
	baseReset := out.Reset
	out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
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
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		if finite && count == 0 && !n.WithTies {
			if state.Retain {
				return false, nil
			}
			return false, child.Close()
		}
		for {
			i, present, err := child.NextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			if offset > 0 {
				offset--
				continue
			}
			if finite && count == 0 {
				peer, err := QueryTiesPeer(ctx, &child, i, n.PeerKeys, boundary, state.Input)
				if err != nil {
					return false, err
				}
				if !peer {
					if state.Retain {
						return false, nil
					}
					return false, child.Close()
				}
			}
			RetainOccurrence(out, &child, i)
			if finite && count > 0 {
				count--
				if count == 0 && n.WithTies {
					CaptureQueryTiesBoundary(&child, i, n.PeerKeys, boundary)
				}
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}

func ValidateProducedValue(value sqltype.Value, usage sqltype.Usage) error {
	if !value.ValidResult(usage) {
		return fmt.Errorf("stuntgres: invalid produced query value")
	}
	return nil
}

// Exclusions leave at most three ordered intervals. Count admitted rows, not
// nonnull values, without allocating or walking the whole frame for each result.
func WindowValueMember(mode parser.FrameExclusion, head, tail, current, peerStart, peerEnd int, ordinal int64, reverse bool) (int, bool) {
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

func evaluateWindowValue(ctx context.Context, evaluation *Context, child *Stage, call plan.WindowCall, rows []int, frame plan.WindowFrame, head, tail, current, peerStart, peerEnd int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	usage := call.Expression.ResultUsage()
	ordinal := int64(0)
	if call.Operation == plan.WindowNth {
		evaluation.BindQueryRow(child.Source, child.Index(rows[current]))
		value, err := call.Ordinal.Eval(evaluation)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := ValidateProducedValue(value, call.Ordinal.ResultUsage()); err != nil {
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
	member, present := WindowValueMember(frame.Exclusion, head, tail, current, peerStart, peerEnd, ordinal, call.Operation == plan.WindowLast)
	if !present {
		return sqltype.Null(usage.Type), nil
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	evaluation.BindQueryRow(child.Source, child.Index(rows[member]))
	value, err := call.Argument.Eval(evaluation)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ValidateProducedValue(value, usage); err != nil {
		return sqltype.Value{}, err
	}
	// Value owns immutable payloads; retaining it in the output overlay neither
	// aliases a reusable scan buffer nor reevaluates/caches the selected member.
	return value, ctx.Err()
}
