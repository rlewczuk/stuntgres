package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/sqltype"
)

// These are closed query stages, not a general table-function provider surface.
type stringTableNode struct {
	querySchema
	calls                  []stringExpression
	ordinality, correlated bool
	frame                  uint32
	viewOrdinal            int
}

func (*stringTableNode) logicalNode() {}

type stringSetStepKind uint8

const (
	stringSetCallStep stringSetStepKind = iota
	stringSetScalarStep
)

type stringSetStep struct {
	kind  stringSetStepKind
	index int
}

// Retained typed operands precede lowering. Canonical execution preflight owns
// simplification and re-lowers surviving occurrences without rebinding syntax.
type stringSetTemplate struct {
	group          *uint32 // Immutable local-group identity and analyzed frame, shared across copies.
	expressions    []BoundExpr
	origins        []BoundExpr
	scalars        []BoundExpr // Typed non-call owner candidates, before lower-marker substitution.
	scalarRequests []int       // Original owner request indices, not source-text identity.
	attempts       bool
	steps          []stringSetStep // Original fixed marker/scalar identities in executable root order.
}

type setProjectNode struct {
	querySchema
	child                  LogicalNode
	calls                  []stringExpression
	scalars                []BoundExpr
	frame                  uint32
	template               *stringSetTemplate
	callSlots              []int // Marker slots relative to the group's fixed base, retained and executable.
	scalarSlots            []int // Sparse candidate slots; nil encodes the ordinary identity layout.
	baseWidth, markerWidth int
	scalarBindings         []stringSetSlot // Execution-private owner roots and fixed scalar slots.
	scalarUpper            bool
	steps                  []stringSetStep // This level's local call/scalar indices, not storage-slot order.
}

func (*setProjectNode) logicalNode() {}

type stringSetCursor struct {
	cursor      sqltype.StringFieldCursor
	exhausted   bool
	initialized bool
}
type stringSetInvocation struct{ cursors []stringSetCursor }

func (s *stringSetInvocation) close() {
	for i := range s.cursors {
		if s.cursors[i].initialized && !s.cursors[i].exhausted {
			s.cursors[i].cursor.Close()
		}
	}
	s.cursors = nil
}
func openStringInvocation(c *EvalContext, calls []stringExpression) (stringSetInvocation, error) {
	s := stringSetInvocation{cursors: make([]stringSetCursor, len(calls))}
	for i, call := range calls {
		cursor, err := openStringCall(c, call)
		if err != nil {
			s.close()
			return stringSetInvocation{}, err
		}
		s.cursors[i].cursor = cursor
		s.cursors[i].initialized = true
	}
	return s, nil
}
func openStringCall(c *EvalContext, call stringExpression) (sqltype.StringFieldCursor, error) {
	var values [4]sqltype.Value
	for j, arg := range call.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.StringFieldCursor{}, err
		}
		values[j] = v
	}
	output := c.Output
	scope := c.liveScope()
	output.Regclass = scope
	output.Regtype = scope
	return call.plan.OpenStringTable(c.Context, values, c.Input, output)
}
func (s *stringSetInvocation) next(ctx context.Context, calls []stringExpression, row []sqltype.Value, slots []int) (bool, error) {
	present := false
	for i := range s.cursors {
		cursor := &s.cursors[i]
		slot := i
		if slots != nil {
			slot = slots[i]
		}
		value, ok, err := cursor.next(ctx, calls[i].usage.Type)
		if err != nil {
			return false, err
		}
		row[slot] = value
		present = present || ok
	}
	return present, ctx.Err()
}

func (s *stringSetCursor) next(ctx context.Context, typ *sqltype.Descriptor) (sqltype.Value, bool, error) {
	if s.exhausted {
		return sqltype.Null(typ), false, nil
	}
	if !s.initialized {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: unopened string set cursor")
	}
	value, present, err := s.cursor.Next(ctx)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if !present {
		s.cursor.Close()
		s.exhausted = true
		return sqltype.Null(typ), false, nil
	}
	return value, true, nil
}
func openStringTableNode(ctx context.Context, state *queryState, n *stringTableNode) (indexedStage, error) {
	source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope}
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}}
	var invocation stringSetInvocation
	opened := false
	ordinal := int64(0)
	frame := make([]sqltype.Value, len(n.Schema()))
	out.finish = func() error { invocation.close(); return nil }
	evaluation := state.evaluation(nil)
	evaluation.Frame = n.frame
	evaluation.Row = state.stringRow
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		evaluation.setContext(ctx)
		if !opened {
			var err error
			if state.stringRecheck != nil {
				invocation, err = state.stringRecheck.cache.open(&evaluation, n)
			} else {
				invocation, err = openStringInvocation(&evaluation, n.calls)
			}
			if err != nil {
				return false, err
			}
			opened = true
		}
		present, err := invocation.next(ctx, n.calls, frame, nil)
		if err != nil {
			return false, err
		}
		if !present {
			invocation.close()
			return false, nil
		}
		if n.ordinality {
			if ordinal == math.MaxInt64 {
				return false, sqlError("22003", "bigint out of range")
			}
			ordinal++
			frame[len(frame)-1], err = sqltype.Integer(n.Schema()[len(frame)-1].Usage.Type, ordinal)
			if err != nil {
				return false, err
			}
		}
		if err := appendStringStageRow(out, frame); err != nil {
			return false, err
		}
		return true, ctx.Err()
	}
	return out, nil
}
func appendStringStageRow(out *indexedStage, frame []sqltype.Value) error {
	source := out.source
	maxInt := int(^uint(0) >> 1)
	if source.flatCount == maxInt || len(source.flat) > maxInt-len(frame) {
		return fmt.Errorf("stuntgres: string set result is too large")
	}
	source.flat = append(source.flat, frame...)
	out.selection = append(out.selection, source.flatCount)
	source.flatCount++
	return nil
}
func openSetProjectNode(ctx context.Context, state *queryState, n *setProjectNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.child)
	if err != nil {
		return indexedStage{}, err
	}
	width := len(n.Schema())
	base, callWidth := n.baseWidth, n.markerWidth
	if base < 0 || callWidth < len(n.calls) || base+callWidth+len(n.scalars) > width ||
		(len(n.child.Schema()) != base && len(n.child.Schema()) != width) || len(n.callSlots) != len(n.calls) || n.scalarSlots != nil && len(n.scalarSlots) != len(n.scalars) {
		child.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: invalid string set frame")
	}
	for i, slot := range n.callSlots {
		if slot < 0 || slot >= callWidth {
			child.Close()
			return indexedStage{}, fmt.Errorf("stuntgres: invalid string set call slot")
		}
		for _, previous := range n.callSlots[:i] {
			if previous == slot {
				child.Close()
				return indexedStage{}, fmt.Errorf("stuntgres: duplicate string set call slot")
			}
		}
	}
	scalarWidth := width - base - callWidth
	for i := range n.scalars {
		slot := stringSetScalarSlot(n.scalarSlots, i)
		if slot < 0 || slot >= scalarWidth || n.scalars[i] == nil || stringSetDepth(n.scalars[i]) != 0 || n.scalars[i].ResultUsage() != n.Schema()[base+callWidth+slot].Usage {
			child.Close()
			return indexedStage{}, fmt.Errorf("stuntgres: invalid string set scalar slot")
		}
		for previous := range i {
			if stringSetScalarSlot(n.scalarSlots, previous) == slot {
				child.Close()
				return indexedStage{}, fmt.Errorf("stuntgres: duplicate string set scalar slot")
			}
		}
	}
	if err := validateStringSetSteps(n.steps, len(n.calls), len(n.scalars)); err != nil {
		child.Close()
		return indexedStage{}, err
	}
	if n.template != nil {
		if err := validateStringSetSteps(n.template.steps, len(n.template.origins), len(n.template.scalars)); err != nil {
			child.Close()
			return indexedStage{}, err
		}
		if err := validateStringSetStageOrder(n.template.steps, n.steps, n.callSlots, n.scalarSlots, len(n.scalars)); err != nil {
			child.Close()
			return indexedStage{}, err
		}
	}
	source := &querySource{flatWidth: width, materialized: true, authority: child.source.authority, scope: child.source.scope, occurrences: child.source.occurrences}
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}, pending: child.pending, cacheWidth: child.cacheWidth}
	var invocation stringSetInvocation
	input := -1
	frame := make([]sqltype.Value, width)
	for i := range width - base {
		frame[base+i] = sqltype.Null(n.Schema()[base+i].Usage.Type)
	}
	evaluation := state.evaluation(nil)
	evaluation.Frame = n.frame
	out.finish = func() error { invocation.close(); return child.Close() }
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		for {
			evaluation.setContext(ctx)
			if input < 0 {
				i, present, err := child.nextOccurrence(ctx)
				if err != nil || !present {
					return present, err
				}
				input = i
				evaluation.Row = child.source.row(child.index(i))
				evaluation.orderValues = child.orderRow(i)
				copy(frame, evaluation.Row)
				invocation = stringSetInvocation{cursors: make([]stringSetCursor, len(n.calls))}
			}
			present := len(n.calls) == 0 // Zero-depth groups have one scalar attempt.
			for _, step := range n.steps {
				if step.kind == stringSetScalarStep {
					value, err := n.scalars[step.index].Eval(&evaluation)
					if err != nil {
						return false, err
					}
					frame[base+callWidth+stringSetScalarSlot(n.scalarSlots, step.index)] = value
					continue
				}
				call := n.calls[step.index]
				cursor := &invocation.cursors[step.index]
				if !cursor.initialized {
					opened, err := openStringCall(&evaluation, call)
					if err != nil {
						return false, err
					}
					cursor.cursor, cursor.initialized = opened, true
				}
				value, field, err := cursor.next(ctx, call.usage.Type)
				if err != nil {
					return false, err
				}
				frame[base+n.callSlots[step.index]] = value
				present = present || field
			}
			if !present {
				invocation.close()
				input = -1
				continue
			}
			if err := appendStringStageRow(out, frame); err != nil {
				return false, err
			}
			if out.cacheWidth != 0 {
				out.orderValues = append(out.orderValues, child.orderRow(input)...)
			}
			if len(source.occurrences) != 0 {
				source.provenance = appendQueryProvenance(source.provenance, child.source, child.index(input))
				if state.source.locks != nil {
					source.versions = appendQueryVersions(source.versions, child.source, child.index(input), state.source.locks)
				}
			}
			if len(n.calls) == 0 {
				input = -1
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}
