package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

const (
	querySetCallStep plan.SetStepKind = iota
	querySetScalarStep
)

type functionSetInvocation struct{ cursors []expr.SourceCursor }

func (s *functionSetInvocation) close() {
	for i := range s.cursors {
		s.cursors[i].Close()
	}
	s.cursors = nil
}
func strictScalarFunctionSource(call expr.Expr) bool {
	source, admitted := call.(expr.Source)
	strict, folds := call.(expr.StrictFolding)
	if !admitted || !folds || strict.StrictNull() != expr.StrictAfter {
		return false
	}
	allowed, set := source.SourceAdmission()
	return allowed && !set && call.ResultUsage().Type != nil && !call.ResultUsage().Type.IsComposite()
}

func preflightFunctionFrom(c *EvalContext, n *plan.FunctionFromNode) (plan.Node, error) {
	// Plan every child before simplifying a source call. Keep the analyzed
	// call carrier: it owns aliases, record expansion and the fixed stage width.
	planned, err := rewriteQueryExpressions(n, func(expr expr.Expr) (expr.Expr, bool, error) {
		return preflightCoercions(c, expr)
	})
	if err != nil {
		return nil, err
	}
	source := planned.(*plan.FunctionFromNode)
	var scalarNull []bool
	for i, call := range source.Calls {
		if !strictScalarFunctionSource(call) {
			continue
		}
		next, _, err := simplifyConditional(c, call, false)
		if err != nil {
			return nil, err
		}
		if randomLiteralNull(next) {
			if scalarNull == nil {
				scalarNull = make([]bool, len(source.Calls))
			}
			scalarNull[i] = true
		}
	}
	if scalarNull == nil && source.ScalarNull == nil {
		return source, nil
	}
	copy := *source
	copy.ScalarNull = scalarNull
	return &copy, nil
}

// openFunctionCall opens call i of a ROWS FROM item; a strict call with a NULL
// scalar argument yields its typed NULL after the node prologue.
func openFunctionCall(c *EvalContext, n *plan.FunctionFromNode, i int) (expr.SourceCursor, error) {
	if n.ScalarNull != nil && n.ScalarNull[i] {
		if err := evaluationContext(c); err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(sqltype.Null(n.Calls[i].ResultUsage().Type)), nil
	}
	return openFunctionSource(c, n.Calls[i])
}

func openFunctionInvocation(c *EvalContext, n *plan.FunctionFromNode) (functionSetInvocation, error) {
	s := functionSetInvocation{cursors: make([]expr.SourceCursor, len(n.Calls))}
	for i := range n.Calls {
		cursor, err := openFunctionCall(c, n, i)
		if err != nil {
			s.close()
			return functionSetInvocation{}, err
		}
		s.cursors[i] = cursor
	}
	return s, nil
}
func openFunctionSource(c *EvalContext, expression expr.Expr) (expr.SourceCursor, error) {
	if !expr.AdmitsSource(expression) {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	opening, ok := expression.(expr.SourceOpening)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	return opening.OpenSource(c)
}

func openFunctionFromNode(ctx context.Context, state *queryState, n *plan.FunctionFromNode) (indexedStage, error) {
	if n.Frame == 0 {
		return indexedStage{}, fmt.Errorf("stuntgres: invalid function source frame")
	}
	if err := validateFunctionFromLayout(n); err != nil {
		return indexedStage{}, err
	}
	source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope}
	establishSourcePresence(source, n.PresenceSlot)
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}}
	var invocation functionSetInvocation
	opened := false
	ordinal := int64(0)
	frame := make([]sqltype.Value, len(n.Schema()))
	out.finish = func() error { invocation.close(); return nil }
	evaluation := state.evaluation(nil)
	evaluation.Frame = n.Frame
	if state.invocation != nil && state.invocation.Scope == evaluation.Scope && state.invocation.Frame == n.Frame {
		evaluation.Row = state.invocation.Row
		evaluation.presenceSlots = state.invocation.presenceSlots
		evaluation.presence = state.invocation.presence
		evaluation.queryOccurrences = state.invocation.queryOccurrences
		evaluation.queryProvenance = state.invocation.queryProvenance
		evaluation.queryCurrentRows = state.invocation.queryCurrentRows
	} else if n.Correlated {
		return indexedStage{}, fmt.Errorf("stuntgres: missing function invocation frame")
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		evaluation.setContext(ctx)
		if !opened {
			var err error
			invocation, err = openFunctionInvocation(&evaluation, n)
			if err != nil {
				return false, err
			}
			opened = true
		}
		present, err := invocation.nextFrom(ctx, n.Calls, frame)
		if err != nil {
			return false, err
		}
		if !present {
			invocation.close()
			return false, nil
		}
		if n.Ordinality {
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
func appendFunctionStageRow(ctx context.Context, out *indexedStage, schema []catalog.Column, row []sqltype.Value) error {
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

func openSetProjectNode(ctx context.Context, state *queryState, n *plan.SetProjectNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, n.Child)
	if err != nil {
		return indexedStage{}, err
	}
	width := len(n.Schema())
	base, callWidth := n.BaseWidth, n.MarkerWidth
	if base < 0 || callWidth < len(n.Calls) || base+callWidth+len(n.Scalars) > width ||
		(len(n.Child.Schema()) != base && len(n.Child.Schema()) != width) || len(n.CallSlots) != len(n.Calls) || n.ScalarSlots != nil && len(n.ScalarSlots) != len(n.Scalars) {
		child.Close()
		return indexedStage{}, fmt.Errorf("stuntgres: invalid string set frame")
	}
	for i, slot := range n.CallSlots {
		if slot < 0 || slot >= callWidth {
			child.Close()
			return indexedStage{}, fmt.Errorf("stuntgres: invalid string set call slot")
		}
		if !sourceReturnsSet(n.Calls[i]) || n.Calls[i].ResultUsage() != n.Schema()[base+slot].Usage {
			child.Close()
			return indexedStage{}, fmt.Errorf("stuntgres: invalid function set call usage")
		}
		for _, previous := range n.CallSlots[:i] {
			if previous == slot {
				child.Close()
				return indexedStage{}, fmt.Errorf("stuntgres: duplicate string set call slot")
			}
		}
	}
	scalarWidth := width - base - callWidth
	for i := range n.Scalars {
		slot := querySetScalarSlot(n.ScalarSlots, i)
		if slot < 0 || slot >= scalarWidth || n.Scalars[i] == nil || querySetDepth(n.Scalars[i]) != 0 || n.Scalars[i].ResultUsage() != n.Schema()[base+callWidth+slot].Usage {
			child.Close()
			return indexedStage{}, fmt.Errorf("stuntgres: invalid string set scalar slot")
		}
		for previous := range i {
			if querySetScalarSlot(n.ScalarSlots, previous) == slot {
				child.Close()
				return indexedStage{}, fmt.Errorf("stuntgres: duplicate string set scalar slot")
			}
		}
	}
	if err := validateQuerySetSteps(n.Steps, len(n.Calls), len(n.Scalars)); err != nil {
		child.Close()
		return indexedStage{}, err
	}
	if n.Template != nil {
		if err := validateQuerySetSteps(n.Template.Steps, len(n.Template.Origins), len(n.Template.Scalars)); err != nil {
			child.Close()
			return indexedStage{}, err
		}
		if err := validateQuerySetStageOrder(n.Template.Steps, n.Steps, n.CallSlots, n.ScalarSlots, len(n.Scalars)); err != nil {
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
	evaluation.Frame = n.Frame
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
				invocation = functionSetInvocation{cursors: make([]expr.SourceCursor, len(n.Calls))}
			}
			present := len(n.Calls) == 0 // Zero-depth groups have one scalar attempt.
			for _, step := range n.Steps {
				if step.Kind == querySetScalarStep {
					value, err := n.Scalars[step.Index].Eval(&evaluation)
					if err != nil {
						return false, err
					}
					frame[base+callWidth+querySetScalarSlot(n.ScalarSlots, step.Index)] = value
					continue
				}
				call := n.Calls[step.Index]
				cursor := &invocation.cursors[step.Index]
				if cursor.Type == nil {
					opened, err := openFunctionSource(&evaluation, call)
					if err != nil {
						return false, err
					}
					*cursor = opened
				}
				value, field, err := cursor.Next(ctx)
				if err != nil {
					return false, err
				}
				frame[base+n.CallSlots[step.Index]] = value
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
			if len(n.Calls) == 0 {
				input = -1
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}

// Result usages are the immutable source of FROM spans. Targets continue to
// occupy one record-valued marker slot; only FROM expands member columns.
func functionFromCallWidth(call expr.Expr) int {
	if typ := call.ResultUsage().Type.BaseType(); typ.IsComposite() {
		return typ.CompositeLen()
	}
	return 1
}

func validateFunctionFromLayout(n *plan.FunctionFromNode) error {
	if len(n.Calls) == 0 {
		return fmt.Errorf("stuntgres: empty function source")
	}
	if n.ScalarNull != nil && len(n.ScalarNull) != len(n.Calls) {
		return fmt.Errorf("stuntgres: invalid function source scalar plan")
	}
	offset := 0
	for i, call := range n.Calls {
		if n.ScalarNull != nil && n.ScalarNull[i] {
			if !strictScalarFunctionSource(call) {
				return fmt.Errorf("stuntgres: invalid function source scalar plan")
			}
		}
		width := functionFromCallWidth(call)
		if !sourceAdmitted(call) || width < 0 || offset+width > len(n.Schema()) {
			return fmt.Errorf("stuntgres: invalid function source layout")
		}
		for j := range width {
			usage := call.ResultUsage()
			if base := usage.Type.BaseType(); base.IsComposite() {
				usage = base.CompositeMember(j).Usage
			}
			if usage != n.Schema()[offset+j].Usage {
				return fmt.Errorf("stuntgres: invalid function source usage")
			}
		}
		offset += width
	}
	if n.Ordinality {
		if offset >= len(n.Schema()) || n.Schema()[offset].Usage.Type.ID() != sqltype.Int8 {
			return fmt.Errorf("stuntgres: invalid function source ordinality")
		}
		offset++
	}
	if offset != len(n.Schema()) {
		return fmt.Errorf("stuntgres: invalid function source width")
	}
	return nil
}

func (s *functionSetInvocation) nextFrom(ctx context.Context, calls []expr.Expr, row []sqltype.Value) (bool, error) {
	present, offset := false, 0
	for i := range s.cursors {
		typ := calls[i].ResultUsage().Type
		value, ok, err := s.cursors[i].Next(ctx)
		if err != nil {
			return false, err
		}
		width := functionFromCallWidth(calls[i])
		if base := typ.BaseType(); base.IsComposite() {
			if ok && (value.Type() != typ || !value.IsNull() && value.CompositeLen() != width) {
				return false, fmt.Errorf("stuntgres: invalid function record")
			}
			for j := range width {
				if ok && !value.IsNull() {
					row[offset+j] = value.CompositeField(j)
				} else {
					row[offset+j] = sqltype.Null(base.CompositeMember(j).Usage.Type)
				}
			}
		} else {
			row[offset] = value
		}
		offset += width
		// Row presence comes from the cursor, even when a composite has no fields.
		present = present || ok
	}
	return present, ctx.Err()
}
