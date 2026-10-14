package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/sqltype"
)

// These are closed query stages, not a general table-function provider surface.
type functionFromNode struct {
	querySchema
	calls                  []BoundExpr
	ordinality, correlated bool
	frame                  uint32
	viewOrdinal            int
	regexpNamespace        bool
	captures               []functionCorrelation
	presenceSlot           int
}

func (*functionFromNode) logicalNode() {}

type querySetStepKind uint8

const (
	querySetCallStep querySetStepKind = iota
	querySetScalarStep
)

type querySetStep struct {
	kind  querySetStepKind
	index int
}

// Retained typed operands precede lowering. Canonical execution preflight owns
// simplification and re-lowers surviving occurrences without rebinding syntax.
type querySetTemplate struct {
	group          *uint32 // Immutable local-group identity and analyzed frame, shared across copies.
	expressions    []BoundExpr
	origins        []BoundExpr
	scalars        []BoundExpr // Typed non-call owner candidates, before lower-marker substitution.
	scalarRequests []int       // Original owner request indices, not source-text identity.
	attempts       bool
	steps          []querySetStep // Original fixed marker/scalar identities in executable root order.
}

type setProjectNode struct {
	querySchema
	child                  LogicalNode
	calls                  []BoundExpr
	scalars                []BoundExpr
	frame                  uint32
	template               *querySetTemplate
	callSlots              []int // Marker slots relative to the group's fixed base, retained and executable.
	scalarSlots            []int // Sparse candidate slots; nil encodes the ordinary identity layout.
	baseWidth, markerWidth int
	scalarBindings         []querySetSlot // Execution-private owner roots and fixed scalar slots.
	scalarUpper            bool
	steps                  []querySetStep // This level's local call/scalar indices, not storage-slot order.
}

func (*setProjectNode) logicalNode() {}

type functionCursorKind uint8

const (
	functionStringCursor functionCursorKind = iota
	functionOwnedSetCursor
	functionScalarCursor
	functionJSONKeysCursor
	functionSeriesCursor
)

type functionSetCursor struct {
	cursor                 sqltype.StringFieldCursor
	keys                   sqltype.JSONKeyCursor
	owned                  []sqltype.Value
	position               int
	scalar                 sqltype.Value
	kind                   functionCursorKind
	exhausted, initialized bool
	current, stop, step    int64
}
type functionSetInvocation struct{ cursors []functionSetCursor }

func (s *functionSetCursor) close() {
	if s.initialized && s.kind == functionStringCursor {
		s.cursor.Close()
	}
	if s.initialized && s.kind == functionJSONKeysCursor {
		s.keys.Close()
	}
	s.owned = nil
	s.exhausted = true
}
func (s *functionSetInvocation) close() {
	for i := range s.cursors {
		s.cursors[i].close()
	}
	s.cursors = nil
}
func openFunctionInvocation(c *EvalContext, calls []BoundExpr) (functionSetInvocation, error) {
	s := functionSetInvocation{cursors: make([]functionSetCursor, len(calls))}
	for i, call := range calls {
		cursor, err := openFunctionCall(c, call)
		if err != nil {
			s.close()
			return functionSetInvocation{}, err
		}
		s.cursors[i] = cursor
	}
	return s, nil
}
func openFunctionCall(c *EvalContext, expression BoundExpr) (functionSetCursor, error) {
	switch call := expression.(type) {
	case seriesExpression:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.args) != 2 && len(call.args) != 3 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid series arguments")
		}
		var values [3]int64
		values[2] = 1
		null := false
		for i, arg := range call.args {
			v, err := arg.Eval(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			if v.IsNull() {
				null = true
			} else {
				values[i], err = v.Integer()
				if err != nil {
					return functionSetCursor{}, err
				}
			}
		}
		cursor := functionSetCursor{kind: functionSeriesCursor, initialized: true, current: values[0], stop: values[1], step: values[2], exhausted: null}
		if null {
			return cursor, nil
		}
		if cursor.step == 0 {
			return functionSetCursor{}, sqlError("22023", "step size cannot equal zero")
		}
		cursor.exhausted = cursor.step > 0 && cursor.current > cursor.stop || cursor.step < 0 && cursor.current < cursor.stop
		return cursor, nil
	case subscriptsExpression:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.args) != 2 && len(call.args) != 3 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid subscripts arguments")
		}
		var values [3]sqltype.Value
		null := false
		for i, arg := range call.args {
			value, err := arg.Eval(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			values[i] = value
			null = null || value.IsNull()
		}
		cursor := functionSetCursor{kind: functionSeriesCursor, initialized: true, exhausted: true, step: 1}
		if null {
			return cursor, nil
		}
		dimension, err := values[1].Integer()
		if err != nil {
			return functionSetCursor{}, err
		}
		if dimension <= 0 || dimension > int64(values[0].ArrayRank()) {
			return cursor, nil
		}
		reverse := false
		if len(call.args) == 3 {
			reverse, err = values[2].Boolean()
			if err != nil {
				return functionSetCursor{}, err
			}
		}
		dim := values[0].ArrayDimension(int(dimension) - 1)
		lower := int64(dim.Lower)
		upper := lower + int64(dim.Length) - 1
		cursor.current, cursor.stop = lower, upper
		if reverse {
			cursor.current, cursor.stop, cursor.step = upper, lower, -1
		}
		cursor.exhausted = dim.Length == 0
		return cursor, nil
	case jsonKeysExpression:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.args) != 1 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid JSON keys arguments")
		}
		value, err := call.args[0].Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		cursor, err := call.plan.Open(c.Context, value)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{keys: cursor, kind: functionJSONKeysCursor, initialized: true}, nil
	case binaryExpression:
		if !validFunctionSourceCall(call) {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
		}
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case regexpSetExpression:
		values, err := call.evaluateSet(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{owned: values, kind: functionOwnedSetCursor, initialized: true}, nil
	case regexpExpression:
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case stringExpression:
		if !isStringSet(call) {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
		}
		var values [4]sqltype.Value
		for j, arg := range call.args {
			v, err := arg.Eval(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			values[j] = v
		}
		output := c.Output
		scope := c.liveScope()
		output.Regclass, output.Regtype = scope, scope
		output.Regrole = scope
		output.Regproc = scope
		output.Operator = scope
		cursor, err := call.plan.OpenStringTable(c.Context, values, c.Input, output)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{cursor: cursor, initialized: true}, nil
	default:
		return functionSetCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
}
func (s *functionSetInvocation) next(ctx context.Context, calls []BoundExpr, row []sqltype.Value, slots []int) (bool, error) {
	present := false
	for i := range s.cursors {
		cursor := &s.cursors[i]
		slot := i
		if slots != nil {
			slot = slots[i]
		}
		value, ok, err := cursor.next(ctx, calls[i].ResultUsage().Type)
		if err != nil {
			return false, err
		}
		row[slot] = value
		present = present || ok
	}
	return present, ctx.Err()
}

func (s *functionSetCursor) next(ctx context.Context, typ *sqltype.Descriptor) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, false, err
	}
	if s.exhausted {
		return sqltype.Null(typ), false, nil
	}
	if !s.initialized {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: unopened function set cursor")
	}
	if s.kind == functionSeriesCursor {
		if s.step > 0 && s.current > s.stop || s.step < 0 && s.current < s.stop {
			s.exhausted = true
			return sqltype.Null(typ), false, nil
		}
		value, err := sqltype.Integer(typ, s.current)
		if err != nil {
			return sqltype.Value{}, false, err
		}
		minimum, maximum := int64(math.MinInt64), int64(math.MaxInt64)
		if typ.ID() == sqltype.Int4 {
			minimum, maximum = math.MinInt32, math.MaxInt32
		}
		if s.step > 0 && s.current > maximum-s.step || s.step < 0 && s.current < minimum-s.step {
			s.exhausted = true
		} else {
			s.current += s.step
		}
		return value, true, nil
	}
	if s.kind == functionOwnedSetCursor {
		if s.position < len(s.owned) {
			value := s.owned[s.position]
			s.position++
			return value, true, nil
		}
		s.exhausted = true
		return sqltype.Null(typ), false, nil
	}
	if s.kind == functionScalarCursor {
		if s.position == 0 {
			s.position++
			return s.scalar, true, nil
		}
		s.exhausted = true
		return sqltype.Null(typ), false, nil
	}
	var value sqltype.Value
	var present bool
	var err error
	if s.kind == functionJSONKeysCursor {
		value, present, err = s.keys.Next(ctx)
	} else {
		value, present, err = s.cursor.Next(ctx)
	}
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if !present {
		s.close()
		return sqltype.Null(typ), false, nil
	}
	return value, true, nil
}
func openFunctionFromNode(ctx context.Context, state *queryState, n *functionFromNode) (indexedStage, error) {
	width := len(n.calls)
	if n.ordinality {
		width++
	}
	if n.frame == 0 || len(n.calls) == 0 || width != len(n.Schema()) {
		return indexedStage{}, fmt.Errorf("stuntgres: invalid function source layout")
	}
	source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope}
	establishSourcePresence(source, n.presenceSlot)
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}}
	var invocation functionSetInvocation
	opened := false
	ordinal := int64(0)
	frame := make([]sqltype.Value, len(n.Schema()))
	out.finish = func() error { invocation.close(); return nil }
	evaluation := state.evaluation(nil)
	evaluation.Frame = n.frame
	if state.invocation != nil && state.invocation.Scope == evaluation.Scope && state.invocation.Frame == n.frame {
		evaluation.Row = state.invocation.Row
		evaluation.presenceSlots = state.invocation.presenceSlots
		evaluation.presence = state.invocation.presence
		evaluation.queryOccurrences = state.invocation.queryOccurrences
		evaluation.queryProvenance = state.invocation.queryProvenance
		evaluation.queryCurrentRows = state.invocation.queryCurrentRows
	} else if n.correlated {
		return indexedStage{}, fmt.Errorf("stuntgres: missing function invocation frame")
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		evaluation.setContext(ctx)
		if !opened {
			var err error
			if state.functionRecheck != nil {
				invocation, err = state.functionRecheck.cache.open(&evaluation, n)
			} else {
				invocation, err = openFunctionInvocation(&evaluation, n.calls)
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
		if err := appendFunctionStageRow(ctx, out, n.Schema(), frame); err != nil {
			return false, err
		}
		return true, ctx.Err()
	}
	return out, nil
}
func appendFunctionStageRow(ctx context.Context, out *indexedStage, schema []ColumnDescriptor, row []sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source := out.source
	if len(row) != source.flatWidth || len(row) != len(schema) {
		return fmt.Errorf("stuntgres: invalid function stage width")
	}
	maxInt := int(^uint(0) >> 1)
	if source.flatCount == maxInt || len(source.flat) > maxInt-len(row) {
		return fmt.Errorf("stuntgres: function result is too large")
	}
	for i, v := range row {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := validateProducedValue(v, schema[i].Usage); err != nil {
			return err
		}
	}
	start := len(source.flat)
	source.flat = append(source.flat, row...)
	if err := ctx.Err(); err != nil {
		source.flat = source.flat[:start]
		return err
	}
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
		if !isQuerySet(n.calls[i]) || n.calls[i].ResultUsage() != n.Schema()[base+slot].Usage {
			child.Close()
			return indexedStage{}, fmt.Errorf("stuntgres: invalid function set call usage")
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
		slot := querySetScalarSlot(n.scalarSlots, i)
		if slot < 0 || slot >= scalarWidth || n.scalars[i] == nil || querySetDepth(n.scalars[i]) != 0 || n.scalars[i].ResultUsage() != n.Schema()[base+callWidth+slot].Usage {
			child.Close()
			return indexedStage{}, fmt.Errorf("stuntgres: invalid string set scalar slot")
		}
		for previous := range i {
			if querySetScalarSlot(n.scalarSlots, previous) == slot {
				child.Close()
				return indexedStage{}, fmt.Errorf("stuntgres: duplicate string set scalar slot")
			}
		}
	}
	if err := validateQuerySetSteps(n.steps, len(n.calls), len(n.scalars)); err != nil {
		child.Close()
		return indexedStage{}, err
	}
	if n.template != nil {
		if err := validateQuerySetSteps(n.template.steps, len(n.template.origins), len(n.template.scalars)); err != nil {
			child.Close()
			return indexedStage{}, err
		}
		if err := validateQuerySetStageOrder(n.template.steps, n.steps, n.callSlots, n.scalarSlots, len(n.scalars)); err != nil {
			child.Close()
			return indexedStage{}, err
		}
	}
	source := &querySource{flatWidth: width, materialized: true, authority: child.source.authority, scope: child.source.scope, occurrences: child.source.occurrences}
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}, pending: child.pending, cacheWidth: child.cacheWidth}
	var invocation functionSetInvocation
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
				evaluation.bindQueryRow(child.source, child.index(i))
				evaluation.orderValues = child.orderRow(i)
				copy(frame, evaluation.Row)
				invocation = functionSetInvocation{cursors: make([]functionSetCursor, len(n.calls))}
			}
			present := len(n.calls) == 0 // Zero-depth groups have one scalar attempt.
			for _, step := range n.steps {
				if step.kind == querySetScalarStep {
					value, err := n.scalars[step.index].Eval(&evaluation)
					if err != nil {
						return false, err
					}
					frame[base+callWidth+querySetScalarSlot(n.scalarSlots, step.index)] = value
					continue
				}
				call := n.calls[step.index]
				cursor := &invocation.cursors[step.index]
				if !cursor.initialized {
					opened, err := openFunctionCall(&evaluation, call)
					if err != nil {
						return false, err
					}
					*cursor = opened
				}
				value, field, err := cursor.next(ctx, call.ResultUsage().Type)
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
			if err := appendFunctionStageRow(ctx, out, n.Schema(), frame); err != nil {
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
