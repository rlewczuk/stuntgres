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
type BoundQuery struct{ Root LogicalNode }
type RowCursor interface {
	Schema() []ColumnDescriptor
	Next(context.Context, []sqltype.Value) (bool, error)
	Close() error
}
type RowResult interface{ RowCursor }

type querySchema []ColumnDescriptor

func (querySchema) logicalNode()                 {}
func (s querySchema) Schema() []ColumnDescriptor { return s }

type sourceNode struct {
	querySchema
	ref ObjectRef
}
type singletonNode struct{ querySchema }
type filterNode struct {
	querySchema
	child     LogicalNode
	predicate BoundExpr
}
type sortNode struct {
	querySchema
	child      LogicalNode
	slot       int
	comparison sqltype.Comparison
	desc       bool
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
	case *sourceNode, *singletonNode:
		return n, nil
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
		child, err := preflightQuery(c, n.child)
		if err != nil {
			return nil, err
		}
		if child != n.child {
			copy := *n
			copy.child = child
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

// querySource retains only the captured immutable prefixes for one relation.
// A nil selection preserves the allocation-free all-occurrences path.
type querySource struct {
	view           *VisibilityView
	ref            ObjectRef
	committed, own []rowVersion
	singleton      bool
}

func captureQuerySource(view *VisibilityView, ref ObjectRef) *querySource {
	pin := view.Retain()
	data := pin.read()
	return &querySource{view: pin, ref: ref, committed: data.committed[ref.Address], own: data.own[ref.Address]}
}
func (s *querySource) count() int {
	if s.singleton {
		return 1
	}
	return len(s.committed) + len(s.own)
}
func (s *querySource) row(i int) []sqltype.Value {
	if s.singleton {
		return nil
	}
	if i < len(s.committed) {
		return s.committed[i].values
	}
	return s.own[i-len(s.committed)].values
}
func (s *querySource) close() {
	if s.view != nil {
		s.view.Release()
	}
	s.view = nil
	s.committed, s.own = nil, nil
}

// Indexed stages retain occurrences, not copied input tuples. All scalar work
// delegates to the sole expression evaluator; each stage owns its private state.
type queryState struct {
	source     *querySource
	parameters []sqltype.Value
}
type indexedStage struct {
	source      *querySource
	selection   []int
	schema      []ColumnDescriptor
	expressions []BoundExpr
	evaluation  EvalContext
	position    int
	terminal    error
	closed      bool
}

func (s indexedStage) count() int {
	if s.selection != nil {
		return len(s.selection)
	}
	return s.source.count()
}
func (s indexedStage) index(i int) int {
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
	if s.position == s.count() {
		s.Close()
		return false, nil
	}
	row := s.source.row(s.index(s.position))
	if s.expressions == nil {
		copy(dst, row)
	} else {
		s.evaluation.Context, s.evaluation.Row = ctx, row
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
	s.evaluation = EvalContext{}
}
func (s *indexedStage) Close() error {
	s.release()
	s.closed = true
	s.terminal = nil
	return nil
}
func openQueryNode(ctx context.Context, state *queryState, node LogicalNode) (indexedStage, error) {
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	switch n := node.(type) {
	case *singletonNode:
		if state.source == nil || !state.source.singleton || state.source.view != nil || state.source.ref != (ObjectRef{}) || len(state.source.committed) != 0 || len(state.source.own) != 0 {
			return indexedStage{}, fmt.Errorf("stuntgres: invalid singleton query source")
		}
		return indexedStage{source: state.source, schema: n.Schema()}, nil
	case *sourceNode:
		if state.source == nil || state.source.view == nil {
			return indexedStage{}, fmt.Errorf("stuntgres: missing query source")
		}
		data := state.source.view.read()
		if n.ref != state.source.ref || n.ref.owner != data.catalog.owner.owner || n.ref.Epoch != data.catalog.Epoch() {
			return indexedStage{}, fmt.Errorf("stuntgres: invalid query source target")
		}
		return indexedStage{source: state.source, schema: n.Schema()}, nil
	case *filterNode:
		child, err := openQueryNode(ctx, state, n.child)
		if err != nil {
			return indexedStage{}, err
		}
		selection := make([]int, 0, child.count())
		evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: state.parameters}
		for i := range child.count() {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			index := child.index(i)
			evaluation.Row = child.source.row(index)
			value, err := n.predicate.Eval(&evaluation)
			if err != nil {
				return indexedStage{}, err
			}
			truth, err := TruthOf(value)
			if err != nil {
				return indexedStage{}, err
			}
			if truth == True {
				selection = append(selection, index)
			}
		}
		child.selection = selection
		return child, nil
	case *sortNode:
		child, err := openQueryNode(ctx, state, n.child)
		if err != nil {
			return indexedStage{}, err
		}
		if child.selection == nil {
			child.selection = make([]int, child.count())
			for i := range child.selection {
				child.selection[i] = i
			}
		}
		err = mergeQueryIndices(ctx, child.selection, func(a, b int) (int, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			left, right := child.source.row(a)[n.slot], child.source.row(b)[n.slot]
			comparison := 0
			if left.IsNull() {
				if !right.IsNull() {
					comparison = 1
				}
			} else if right.IsNull() {
				comparison = -1
			} else {
				var err error
				comparison, err = n.comparison.Compare(ctx, left, right)
				if err != nil {
					return 0, err
				}
			}
			if n.desc {
				comparison = -comparison
			}
			return comparison, ctx.Err()
		})
		return child, err
	case *projectNode:
		child, err := openQueryNode(ctx, state, n.child)
		if err != nil {
			return indexedStage{}, err
		}
		child.schema = n.Schema()
		child.expressions = n.expressions
		child.evaluation = EvalContext{Scope: 1, Frame: 1, Parameters: state.parameters}
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
	anonymous         [][]sqltype.Value
	count, position   int
	terminal          error
	exhausted, closed bool
}

func newFinalRows(schema []ColumnDescriptor, count int) *finalRows {
	r := &finalRows{schema: schema, count: count}
	r.self = r
	return r
}
func (r *finalRows) Schema() []ColumnDescriptor { return r.schema }
func (r *finalRows) release() {
	if r.source != nil {
		r.source.close()
		r.source = nil
	}
	r.selection, r.slots, r.values = nil, nil, nil
	r.anonymous = nil
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

// finalizeQuery consumes an already-preflighted execution-private root.
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
	stage, err := openQueryNode(ctx, &queryState{source: source, parameters: parameters}, project)
	if err != nil {
		return nil, 0, err
	}
	defer stage.Close()
	r := newFinalRows(project.Schema(), stage.count())
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
		evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: parameters}
		for i := range r.count {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			evaluation.Row = source.row(stage.index(i))
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
	if err := stage.Close(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if direct && r.count > 0 {
		r.source, r.selection, r.slots = source, selection, slots
		keep = true
	}
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
