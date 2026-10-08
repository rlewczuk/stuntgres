package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

// LogicalNode is a closed, immutable bound tree. Execution never resolves names.
type LogicalNode interface {
	logicalNode()
	Schema() []ColumnDescriptor
}
type BoundQuery struct {
	Root           LogicalNode
	regclassOutput bool
	// Local expression demand only; inherited view demand uses the current catalog.
	visibilitySnapshot bool
}
type RowCursor interface {
	Schema() []ColumnDescriptor
	Next(context.Context, []sqltype.Value) (bool, error)
	Close() error
}
type RowResult interface {
	RowCursor
	RegclassText(sqltype.Value) (string, error)
	OutputEnvironment() sqltype.OutputEnvironment
}

type querySchema []ColumnDescriptor

func (querySchema) logicalNode()                 {}
func (s querySchema) Schema() []ColumnDescriptor { return s }

type sourceNode struct {
	querySchema
	ref        ObjectRef
	occurrence int
}
type singletonNode struct{ querySchema }
type valuesNode struct {
	querySchema
	rows                [][]BoundExpr
	identityOrderDemand []bool // Only singleton USER identity VALUES need pruning.
}
type filterNode struct {
	querySchema
	child     LogicalNode
	predicate BoundExpr
}
type sortKey struct {
	expression       BoundExpr
	comparison       sqltype.Comparison
	position         int // Original ORDER reference character position, not reused output syntax.
	slot, cache      int
	desc, nullsFirst bool
	output           bool // This cache slot supplies a visible projection.
}
type sortNode struct {
	querySchema
	child      LogicalNode
	keys       []sortKey
	cacheWidth int
	outputs    []BoundExpr // Cheap immutable targets required by the sort input.
}
type projectNode struct {
	querySchema
	child       LogicalNode
	expressions []BoundExpr
}

// preflightQuery retains schemas and source identity from analysis. Only paths
// containing folded coercions are copied for this execution.
func preflightQuery(c *EvalContext, node LogicalNode) (LogicalNode, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	switch n := node.(type) {
	case *limitNode:
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		// Bounds retain their ordered runtime coercion and value-check frontier.
		// Folding COUNT here could overtake a failing OFFSET.
		if child != n.child {
			copy := *n
			copy.child = child
			return &copy, nil
		}
		return n, nil
	case *evaluationNode:
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		var expressions []BoundExpr
		for i, expr := range n.expressions {
			folded, changed, err := preflightCoercions(c, expr)
			if err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = append([]BoundExpr(nil), n.expressions...)
				}
				expressions[i] = folded
			}
		}
		if child != n.child || expressions != nil {
			copy := *n
			copy.child = child
			if expressions != nil {
				copy.expressions = expressions
			}
			return &copy, nil
		}
		return n, nil
	case *rowLockNode:
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		if child != n.child {
			copy := *n
			copy.child = child
			return &copy, nil
		}
		return n, nil
	case *joinNode:
		return preflightJoin(c, n)
	case *sourceNode, *singletonNode:
		return n, nil
	case *valuesNode:
		var rows [][]BoundExpr
		for i, row := range n.rows {
			var expressions []BoundExpr
			for j, expression := range row {
				folded, changed, err := preflightCoercions(c, expression)
				if err != nil {
					return nil, err
				}
				if changed {
					if expressions == nil {
						expressions = append([]BoundExpr(nil), row...)
					}
					expressions[j] = folded
				}
			}
			if expressions != nil {
				if rows == nil {
					rows = append([][]BoundExpr(nil), n.rows...)
				}
				rows[i] = expressions
			}
		}
		if rows != nil {
			copy := *n
			copy.rows = rows
			return &copy, nil
		}
	case *aggregateNode:
		var keys []BoundExpr
		for i, key := range n.keys {
			folded, replacement, err := preflightCoercions(c, key)
			if err != nil {
				return nil, err
			}
			if replacement {
				if keys == nil {
					keys = append([]BoundExpr(nil), n.keys...)
				}
				keys[i] = folded
			}
		}
		var calls []aggregateCall
		for i, call := range n.calls {
			if call.argument == nil {
				continue
			}
			folded, replacement, err := preflightCoercions(c, call.argument)
			if err != nil {
				return nil, err
			}
			if replacement {
				if calls == nil {
					calls = append([]aggregateCall(nil), n.calls...)
				}
				calls[i].argument = folded
			}
		}
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		if keys != nil || calls != nil || child != n.child {
			copy := *n
			copy.child = child
			if keys != nil {
				copy.keys = keys
			}
			if calls != nil {
				copy.calls = calls
			}
			return &copy, nil
		}
	case *filterNode:
		predicate, changed, err := preflightCoercions(c, n.predicate)
		if err != nil {
			return nil, err
		}
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		if changed || child != n.child {
			copy := *n
			copy.child, copy.predicate = child, predicate
			return &copy, nil
		}
	case *sortNode:
		var keys []sortKey
		for i, key := range n.keys {
			folded, changed, err := preflightCoercions(c, key.expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if keys == nil {
					keys = append([]sortKey(nil), n.keys...)
				}
				keys[i].expression = folded
			}
		}
		var outputs []BoundExpr
		for i, expr := range n.outputs {
			folded, changed, err := preflightCoercions(c, expr)
			if err != nil {
				return nil, err
			}
			if changed {
				if outputs == nil {
					outputs = append([]BoundExpr(nil), n.outputs...)
				}
				outputs[i] = folded
			}
		}
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		if child != n.child || keys != nil || outputs != nil {
			copy := *n
			copy.child = child
			if keys != nil {
				copy.keys = keys
			}
			if outputs != nil {
				copy.outputs = outputs
			}
			return &copy, nil
		}
	case *projectNode:
		var expressions []BoundExpr
		for i, expression := range n.expressions {
			folded, changed, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = append([]BoundExpr(nil), n.expressions...)
				}
				expressions[i] = folded
			}
		}
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		if expressions != nil || child != n.child {
			copy := *n
			copy.child = child
			if expressions != nil {
				copy.expressions = expressions
			}
			return &copy, nil
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid logical query node")
	}
	return node, nil
}

// A leaf retains immutable relation-local prefixes; the first leaf also owns
// the occurrence bundle. Joined stages instead own flat admitted frames.
// A nil visible mapping preserves the allocation-free all-occurrences path.
type querySource struct {
	view                 *VisibilityView
	ref                  ObjectRef
	committed, own       []rowVersion
	singleton            bool
	anonymous            [][]sqltype.Value
	visible              []int
	scope                *StatementScope
	sources              []*querySource
	flat                 []sqltype.Value
	flatWidth, flatCount int
	materialized         bool
	final                *finalRows
	input                *querySource
	layout               []int
	scratch              []sqltype.Value
	locks                *queryLockContext
	lineage              *queryLockLineage
	nestedRoot           LogicalNode
	nestedInputs         *querySource
	nestedStage          *indexedStage
	// Only locking joins populate provenance; ordinary query work is unchanged.
	occurrence          int
	occurrences         []int
	provenance          []int
	versions            []rowVersion
	joinLeft, joinRight *querySource
	joinPairs           []queryPair
}

func (e *Engine) captureSequenceSourceLocked(scope *StatementScope, ref ObjectRef, relation *relationDescriptor) (*querySource, error) {
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return nil, fmt.Errorf("stuntgres: sequence state missing")
	}
	i := relation.columns[0].Usage.Type
	last, _ := sqltype.Integer(i, g.last)
	log, _ := sqltype.Integer(i, 0)
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{{last, log, sqltype.Boolean(relation.columns[2].Usage.Type, g.called)}}}, nil
}
func (s *querySource) count() int {
	if s.materialized {
		return s.flatCount
	}
	if s.final != nil {
		return s.final.count
	}
	if s.input != nil {
		return s.input.count()
	}
	if s.anonymous != nil {
		return len(s.anonymous)
	}
	if s.singleton {
		return 1
	}
	if s.visible != nil {
		return len(s.visible)
	}
	return len(s.committed) + len(s.own)
}
func (s *querySource) row(i int) []sqltype.Value {
	if s.materialized {
		if s.anonymous != nil {
			return s.anonymous[i]
		}
		return s.flat[i*s.flatWidth : (i+1)*s.flatWidth]
	}
	if s.input != nil {
		row := s.input.row(i)
		for j, slot := range s.layout {
			if slot >= 0 {
				s.scratch[j] = row[slot]
			}
		}
		return s.scratch
	}
	if s.final != nil {
		r := s.final
		if r.source != nil {
			index := i
			if r.selection != nil {
				index = r.selection[i]
			}
			row := r.source.row(index)
			for j, slot := range r.slots {
				s.scratch[j] = row[slot]
			}
			return s.scratch
		}
		if r.anonymous != nil {
			return r.anonymous[i]
		}
		width := len(r.schema)
		return r.values[i*width : (i+1)*width]
	}
	if s.anonymous != nil {
		return s.anonymous[i]
	}
	if s.singleton {
		return nil
	}
	return s.versionAt(s.rawIndex(i)).values
}
func (s *querySource) rawIndex(i int) int {
	if s.visible != nil {
		return s.visible[i]
	}
	return i
}
func (s *querySource) versionAt(i int) rowVersion {
	if i < len(s.committed) {
		return s.committed[i]
	}
	return s.own[i-len(s.committed)]
}
func (s *querySource) close() {
	sources := s.sources
	s.sources = nil
	if s.nestedStage != nil {
		s.nestedStage.Close()
		s.nestedStage = nil
	}
	s.nestedRoot = nil
	if s.nestedInputs != nil {
		s.nestedInputs.close()
		s.nestedInputs = nil
	}
	for _, child := range sources {
		if child != nil && child != s {
			child.close()
		}
	}
	s.flat = nil
	s.occurrences = nil
	s.provenance = nil
	s.versions = nil
	s.joinLeft, s.joinRight, s.joinPairs = nil, nil, nil
	s.flatCount = 0
	s.flatWidth = 0
	s.materialized = false
	lineage := s.lineage
	s.lineage = nil
	stageClosed := lineage != nil && s.final != nil && s.final.source == lineage.stage
	if s.final != nil {
		s.final.Close()
		s.final = nil
	}
	if lineage != nil {
		if !stageClosed {
			lineage.stage.close()
		}
		if lineage.inputs != lineage.stage {
			lineage.inputs.close()
		}
	}
	s.locks = nil
	if s.input != nil {
		s.input.close()
		s.input = nil
	}
	s.scratch, s.layout = nil, nil
	if s.view != nil {
		s.view.Release()
	}
	s.view = nil
	s.committed, s.own = nil, nil
	s.anonymous = nil
	s.visible = nil
}

// Indexed stages retain occurrences, not copied input tuples. All scalar work
// delegates to the sole expression evaluator; each stage owns its private state.
type queryState struct {
	source     *querySource
	parameters []sqltype.Value
	input      sqltype.InputEnvironment
	output     sqltype.OutputEnvironment
	locking    bool
}
type indexedStage struct {
	source           *querySource
	selection        []int
	permutation      []int
	orderValues      []sqltype.Value
	cacheOccurrences []int
	cacheWidth       int
	pending          []queryPendingMark
	schema           []ColumnDescriptor
	expressions      []BoundExpr
	evaluation       EvalContext
	position         int
	terminal         error
	closed           bool
	produce          func(context.Context, *indexedStage) (bool, error)
	finish           func() error
	done             bool
}

func (s indexedStage) count() int {
	if s.selection != nil {
		return len(s.selection)
	}
	return s.source.count()
}
func (s indexedStage) index(i int) int {
	if s.permutation != nil {
		i = s.permutation[i]
	}
	if s.selection != nil {
		return s.selection[i]
	}
	return i
}
func (s *indexedStage) Schema() []ColumnDescriptor { return s.schema }
func (s *indexedStage) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if s.closed {
		return false, nil
	}
	if s.terminal != nil {
		return false, s.terminal
	}
	fail := func(err error) (bool, error) { s.terminal = err; s.release(); return false, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if len(dst) != len(s.schema) {
		return fail(fmt.Errorf("stuntgres: invalid stage destination width"))
	}
	present, err := s.ensure(ctx, s.position)
	if err != nil {
		return fail(err)
	}
	if !present {
		if err := s.Close(); err != nil {
			return false, err
		}
		return false, nil
	}
	row := s.source.row(s.index(s.position))
	if s.expressions == nil {
		copy(dst, row)
	} else {
		s.evaluation.Context, s.evaluation.Row = ctx, row
		if s.cacheWidth != 0 {
			s.evaluation.orderValues = s.orderRow(s.position)
		}
		for i, expr := range s.expressions {
			value, err := expr.Eval(&s.evaluation)
			if err != nil {
				return fail(err)
			}
			if value.Type() == nil || !value.Type().Concrete() || value.Type() != s.schema[i].Usage.Type {
				return fail(fmt.Errorf("stuntgres: invalid finalized query value"))
			}
			dst[i] = value
		}
	}
	s.position++
	return true, nil
}
func (s *indexedStage) release() {
	// The query execution owner, not this borrowed stage, owns the view pin.
	s.source = nil
	s.selection = nil
	s.expressions = nil
	s.permutation = nil
	s.orderValues = nil
	s.cacheOccurrences = nil
	s.pending = nil
	s.evaluation = EvalContext{}
}
func (s *indexedStage) Close() error {
	if s.closed {
		return nil
	}
	var err error
	if s.finish != nil {
		finish := s.finish
		s.finish = nil
		err = finish()
	}
	s.produce = nil
	s.release()
	s.closed = true
	s.terminal = nil
	return err
}
func openQueryNode(ctx context.Context, state *queryState, node LogicalNode) (indexedStage, error) {
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	switch n := node.(type) {
	case *limitNode:
		return openLimitNode(ctx, state, n)
	case *evaluationNode:
		return openEvaluationNode(ctx, state, n)
	case *rowLockNode:
		return openRowLockNode(ctx, state, n)
	case *joinNode:
		return openJoinNode(ctx, state, n)
	case *aggregateNode:
		return openAggregateNode(ctx, state, n)
	case *valuesNode:
		if state.source == nil || state.source.view != nil {
			return indexedStage{}, fmt.Errorf("stuntgres: invalid VALUES query source")
		}
		source := &querySource{flatWidth: len(n.Schema()), materialized: true}
		out := indexedStage{source: source, schema: n.Schema(), selection: []int{}}
		evaluation := EvalContext{Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: state.source.scope, Input: state.input, Output: state.output}
		out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
			i := source.flatCount
			if i == len(n.rows) {
				return false, nil
			}
			evaluation.Context = ctx
			for j, expr := range n.rows[i] {
				value, err := expr.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				if err := validateProducedValue(value, n.Schema()[j].Usage); err != nil {
					return false, err
				}
				source.flat = append(source.flat, value)
			}
			out.selection = append(out.selection, i)
			source.flatCount++
			return true, ctx.Err()
		}
		return out, nil
	case *singletonNode:
		if state.source == nil || !state.source.singleton || state.source.view != nil || state.source.ref != (ObjectRef{}) || len(state.source.committed) != 0 || len(state.source.own) != 0 || state.source.visible != nil {
			return indexedStage{}, fmt.Errorf("stuntgres: invalid singleton query source")
		}
		return indexedStage{source: state.source, schema: n.Schema()}, nil
	case *sourceNode:
		source := state.source
		if source != nil && source.sources != nil {
			if n.occurrence < 0 || n.occurrence >= len(source.sources) {
				return indexedStage{}, fmt.Errorf("stuntgres: invalid query occurrence")
			}
			source = source.sources[n.occurrence]
		}
		if source == nil || source.ref != n.ref {
			return indexedStage{}, fmt.Errorf("stuntgres: invalid query source target")
		}
		if source.final != nil || source.input != nil || source.nestedRoot != nil {
			return openSourceStage(ctx, state, source, n.Schema())
		}
		if source.anonymous == nil {
			if source.view == nil {
				return indexedStage{}, fmt.Errorf("stuntgres: missing query source")
			}
			data := source.view.read()
			if n.ref.owner != data.catalog.owner.owner || n.ref.Epoch != data.catalog.Epoch() {
				return indexedStage{}, fmt.Errorf("stuntgres: invalid query source target")
			}
		}
		return indexedStage{source: source, schema: n.Schema()}, nil
	case *filterNode:
		child, err := openQueryNode(ctx, state, n.child)
		if err != nil {
			return indexedStage{}, err
		}
		out := producingStage(&child, n.Schema())
		evaluation := EvalContext{Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: state.source.scope, Input: state.input, Output: state.output}
		out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
			for {
				i, present, err := child.nextOccurrence(ctx)
				if err != nil || !present {
					return present, err
				}
				evaluation.Context, evaluation.Row = ctx, child.source.row(child.index(i))
				value, err := n.predicate.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				truth, err := TruthOf(value)
				if err != nil {
					return false, err
				}
				if truth == True {
					retainOccurrence(out, &child, i)
					return true, ctx.Err()
				}
			}
		}
		return out, nil
	case *sortNode:
		child, err := openQueryNode(ctx, state, n.child)
		if err != nil {
			return indexedStage{}, err
		}
		child, err = settlePendingQueryMarks(ctx, state, n.child, child)
		if err != nil {
			return indexedStage{}, err
		}
		if err := child.drain(ctx); err != nil {
			child.Close()
			return indexedStage{}, err
		}
		if child.selection == nil {
			child.selection = make([]int, child.count())
			for i := range child.selection {
				child.selection[i] = i
			}
		}
		if n.cacheWidth != 0 {
			child.cacheWidth = n.cacheWidth
			child.cacheOccurrences = nil
			child.orderValues = make([]sqltype.Value, child.count()*n.cacheWidth)
			child.permutation = make([]int, child.count())
			evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: state.source.scope, Input: state.input, Output: state.output}
			for i := range child.count() {
				child.permutation[i] = i
				evaluation.Row = child.source.row(child.selection[i])
				for _, key := range n.keys {
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
					if key.cache < 0 || child.orderValues[i*n.cacheWidth+key.cache].Type() != nil {
						continue
					}
					value, err := key.expression.Eval(&evaluation)
					if err != nil {
						return indexedStage{}, err
					}
					if value.Type() != key.expression.ResultUsage().Type {
						return indexedStage{}, fmt.Errorf("stuntgres: invalid ordering value")
					}
					child.orderValues[i*n.cacheWidth+key.cache] = value
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
				}
			}
			for i := range child.count() {
				evaluation.Row = child.source.row(child.selection[i])
				for j, expr := range n.outputs {
					value, err := expr.Eval(&evaluation)
					if err != nil {
						return indexedStage{}, err
					}
					child.orderValues[i*n.cacheWidth+n.cacheWidth-len(n.outputs)+j] = value
				}
			}
		}
		indices := child.selection
		if child.permutation != nil {
			indices = child.permutation
		}
		err = mergeQueryIndices(ctx, indices, func(a, b int) (int, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			leftIndex, rightIndex := a, b
			if child.permutation != nil {
				leftIndex, rightIndex = child.selection[a], child.selection[b]
			}
			for _, key := range n.keys {
				var left, right sqltype.Value
				if key.cache >= 0 {
					left, right = child.orderValues[a*n.cacheWidth+key.cache], child.orderValues[b*n.cacheWidth+key.cache]
				} else {
					// View sources may reuse one scratch row. Capture the first
					// datum before requesting the second borrowed row.
					left = child.source.row(leftIndex)[key.slot]
					right = child.source.row(rightIndex)[key.slot]
				}
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
		})
		return child, err
	case *projectNode:
		child, err := openQueryNode(ctx, state, n.child)
		if err != nil {
			return indexedStage{}, err
		}
		child.schema = n.Schema()
		child.expressions = n.expressions
		child.evaluation = EvalContext{Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: state.source.scope, Input: state.input, Output: state.output}
		return child, nil
	default:
		return indexedStage{}, fmt.Errorf("stuntgres: invalid logical query node")
	}
}

// Unlike a bool sort callback, every comparison can stop production with its
// original error. One scratch buffer serves every merge level.
func mergeQueryIndices(ctx context.Context, indices []int, compare func(int, int) (int, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(indices) < 2 {
		return nil
	}
	scratch := make([]int, len(indices))
	var merge func(int, int) error
	merge = func(lo, hi int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if hi-lo < 2 {
			return nil
		}
		mid := lo + (hi-lo)/2
		if err := merge(lo, mid); err != nil {
			return err
		}
		if err := merge(mid, hi); err != nil {
			return err
		}
		i, j, k := lo, mid, lo
		for i < mid && j < hi {
			if err := ctx.Err(); err != nil {
				return err
			}
			order, err := compare(indices[i], indices[j])
			if err != nil {
				return err
			}
			if order <= 0 {
				scratch[k] = indices[i]
				i++
			} else {
				scratch[k] = indices[j]
				j++
			}
			k++
		}
		k += copy(scratch[k:hi], indices[i:mid])
		copy(scratch[k:hi], indices[j:hi])
		copy(indices[lo:hi], scratch[lo:hi])
		return ctx.Err()
	}
	return merge(0, len(indices))
}

// finalRows contains only finalized output, never expressions or parameters.
// self prevents forged or copied handles from releasing another cursor's pins.
type finalRows struct {
	self              *finalRows
	schema            []ColumnDescriptor
	source            *querySource
	selection, slots  []int
	values            []sqltype.Value
	regclassOutput    map[string]string
	anonymous         [][]sqltype.Value
	count, position   int
	terminal          error
	exhausted, closed bool
	output            sqltype.OutputEnvironment
}

func newFinalRows(schema []ColumnDescriptor, count int) *finalRows {
	r := &finalRows{schema: schema, count: count}
	r.self = r
	return r
}
func (r *finalRows) Schema() []ColumnDescriptor                   { return r.schema }
func (r *finalRows) OutputEnvironment() sqltype.OutputEnvironment { return r.output }
func (r *finalRows) RegclassText(v sqltype.Value) (string, error) {
	key, err := v.Text()
	if err != nil {
		return "", err
	}
	text, ok := r.regclassOutput[key]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
	}
	return text, nil
}
func (r *finalRows) release() {
	if r.source != nil {
		r.source.close()
		r.source = nil
	}
	r.selection, r.slots, r.values = nil, nil, nil
	r.anonymous = nil
	r.regclassOutput = nil
}
func (r *finalRows) fail(err error) (bool, error) {
	r.terminal = err
	r.release()
	return false, err
}
func (r *finalRows) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if r == nil || r.self != r {
		return false, fmt.Errorf("stuntgres: invalid result owner")
	}
	if r.closed || r.exhausted {
		return false, nil
	}
	if r.terminal != nil {
		return false, r.terminal
	}
	if err := ctx.Err(); err != nil {
		return r.fail(err)
	}
	if len(dst) != len(r.schema) {
		return r.fail(fmt.Errorf("stuntgres: invalid result destination width"))
	}
	if r.position == r.count {
		r.exhausted = true
		r.release()
		return false, nil
	}
	if r.source != nil {
		index := r.position
		if r.selection != nil {
			index = r.selection[index]
		}
		row := r.source.row(index)
		for i, slot := range r.slots {
			dst[i] = row[slot]
		}
	} else {
		if r.anonymous != nil {
			copy(dst, r.anonymous[r.position])
		} else {
			copy(dst, r.values[r.position*len(dst):(r.position+1)*len(dst)])
		}
	}
	r.position++
	return true, nil
}
func (r *finalRows) Close() error {
	if r != nil && r.self == r && !r.closed {
		r.release()
		r.terminal = nil
		r.closed = true
	}
	return nil
}

// SELECT and INSERT settle residual qualified marks at the eager publication
// frontier. A nested scalar bridge is not a publication frontier.
func openQueryProjectStage(ctx context.Context, state *queryState, project *projectNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, project)
	if err != nil {
		return indexedStage{}, err
	}
	return settlePendingQueryMarks(ctx, state, project.child, child)
}

func finalizeQuery(ctx context.Context, root LogicalNode, source *querySource, parameters []sqltype.Value) (RowResult, int, error) {
	keep := false
	defer func() {
		if !keep {
			source.close()
		}
	}()
	project, ok := root.(*projectNode)
	if !ok {
		return nil, 0, fmt.Errorf("stuntgres: query lacks final projection")
	}
	state := &queryState{source: source, parameters: parameters, locking: source.locks != nil}
	if source.scope != nil {
		state.input, state.output = source.scope.input, source.scope.output
	}
	stage, err := openQueryProjectStage(ctx, state, project)
	if err != nil {
		return nil, 0, err
	}
	defer stage.Close()
	if err := stage.drain(ctx); err != nil {
		return nil, 0, err
	}
	r := newFinalRows(project.Schema(), stage.count())
	r.output = state.output
	var slots []int
	direct := true
	for _, expr := range project.expressions {
		column, ok := expr.(columnExpression)
		if !ok {
			direct = false
			break
		}
		if column.scope != 1 || column.frame != 1 || column.slot < 0 || column.slot >= len(project.child.Schema()) {
			return nil, 0, fmt.Errorf("stuntgres: invalid query projection frame")
		}
	}
	width := len(project.Schema())
	if width != len(project.expressions) {
		return nil, 0, fmt.Errorf("stuntgres: invalid query projection width")
	}
	if !direct {
		r.values = make([]sqltype.Value, r.count*width)
	}
	if direct {
		slots = make([]int, len(project.expressions))
		for i, expr := range project.expressions {
			slots[i] = expr.(columnExpression).slot
		}
		evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: parameters, Input: state.input, Output: state.output}
		for i := range r.count {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			evaluation.Row = stage.source.row(stage.index(i))
			for j, expr := range project.expressions {
				value, err := expr.Eval(&evaluation)
				if err != nil {
					return nil, 0, err
				}
				if value.Type() == nil || !value.Type().Concrete() || value.Type() != r.schema[j].Usage.Type {
					return nil, 0, fmt.Errorf("stuntgres: invalid finalized query value")
				}
			}
		}
	} else {
		for i := range r.count {
			present, err := stage.Next(ctx, r.values[i*width:(i+1)*width])
			if err != nil {
				return nil, 0, err
			}
			if !present {
				return nil, 0, fmt.Errorf("stuntgres: early projection EOF")
			}
		}
	}
	selection := stage.selection
	if stage.permutation != nil && direct {
		// Final physical output identity is distinct from sort-cache identity.
		selection = stage.permutation
		if stage.selection != nil {
			for i, occurrence := range selection {
				selection[i] = stage.selection[occurrence]
			}
		}
	}
	stageSource := stage.source
	if err := stage.Close(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if direct && r.count > 0 {
		r.source, r.selection, r.slots = stageSource, selection, slots
		keep = true
		if stageSource != source {
			source.close()
		}
	}
	detachQuerySource(stageSource)
	return r, r.count, nil
}

// Anonymous producers transfer their already-owned typed rows into the same
// final cursor contract; empty relations still carry an explicit schema.
func anonymousResult(schema []ColumnDescriptor, rows [][]sqltype.Value, count int64) (Result, error) {
	r := newFinalRows(schema, len(rows))
	r.anonymous = rows
	for _, row := range rows {
		if len(row) != len(schema) {
			return Result{}, fmt.Errorf("stuntgres: invalid anonymous result width")
		}
		for j, value := range row {
			if value.Type() == nil || !value.Type().Concrete() || value.Type() != schema[j].Usage.Type {
				return Result{}, fmt.Errorf("stuntgres: invalid anonymous result type")
			}
		}
	}
	return Result{Count: count, Rows: r}, nil
}
