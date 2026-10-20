package exec

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func QuerySetDepth(bound expr.Expr) int {
	depth := 0
	plan.ExpressionChildren(bound, func(child expr.Expr) {
		if d := QuerySetDepth(child); d > depth {
			depth = d
		}
	})
	if SourceReturnsSet(bound) {
		depth++
	}
	return depth
}

func QuerySetScalarSlot(slots []int, index int) int {
	if slots == nil {
		return index
	}
	return slots[index]
}

func StringSetLocalStep(step plan.SetStep, callSlots, scalarSlots []int, scalars int) (plan.SetStep, bool) {
	slots := callSlots
	switch step.Kind {
	case QuerySetCallStep:
	case QuerySetScalarStep:
		if scalarSlots == nil {
			return step, step.Index >= 0 && step.Index < scalars
		}
		slots = scalarSlots
	default:
		return plan.SetStep{}, false
	}
	for i, slot := range slots {
		if slot == step.Index {
			step.Index = i
			return step, true
		}
	}
	return plan.SetStep{}, false
}

func ValidateQuerySetSteps(steps []plan.SetStep, calls, scalars int) error {
	if len(steps) != calls+scalars {
		return fmt.Errorf("stuntgres: incomplete string set schedule")
	}
	for i, step := range steps {
		width := calls
		switch step.Kind {
		case QuerySetCallStep:
		case QuerySetScalarStep:
			width = scalars
		default:
			return fmt.Errorf("stuntgres: invalid string set schedule kind")
		}
		if step.Index < 0 || step.Index >= width {
			return fmt.Errorf("stuntgres: invalid string set schedule index")
		}
		for _, previous := range steps[:i] {
			if previous == step {
				return fmt.Errorf("stuntgres: duplicate string set schedule event")
			}
		}
	}
	return nil
}

func ValidateQuerySetStageOrder(original, executable []plan.SetStep, callSlots, scalarSlots []int, scalars int) error {
	position := 0
	for _, original := range original {
		step, present := StringSetLocalStep(original, callSlots, scalarSlots, scalars)
		if !present {
			continue
		}
		if position >= len(executable) || executable[position] != step {
			return fmt.Errorf("stuntgres: invalid string set schedule order")
		}
		position++
	}
	if position != len(executable) {
		return fmt.Errorf("stuntgres: invalid string set schedule owner")
	}
	return nil
}

const (
	QuerySetCallStep plan.SetStepKind = iota
	QuerySetScalarStep
)

type FunctionSetInvocation struct{ Cursors []expr.SourceCursor }

func (s *FunctionSetInvocation) Close() {
	for i := range s.Cursors {
		s.Cursors[i].Close()
	}
	s.Cursors = nil
}

func StrictScalarFunctionSource(call expr.Expr) bool {
	source, admitted := call.(expr.Source)
	strict, folds := call.(expr.StrictFolding)
	if !admitted || !folds || strict.StrictNull() != expr.StrictAfter {
		return false
	}
	allowed, set := source.SourceAdmission()
	return allowed && !set && call.ResultUsage().Type != nil && !call.ResultUsage().Type.IsComposite()
}

// OpenFunctionCall opens call i of a ROWS FROM item; a strict call with a NULL
// scalar argument yields its typed NULL after the node prologue.
func OpenFunctionCall(c *Context, n *plan.FunctionFromNode, i int) (expr.SourceCursor, error) {
	if n.ScalarNull != nil && n.ScalarNull[i] {
		if err := EvaluationContext(c); err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(sqltype.Null(n.Calls[i].ResultUsage().Type)), nil
	}
	return OpenFunctionSource(c, n.Calls[i])
}

func OpenFunctionInvocation(c *Context, n *plan.FunctionFromNode) (FunctionSetInvocation, error) {
	s := FunctionSetInvocation{Cursors: make([]expr.SourceCursor, len(n.Calls))}
	for i := range n.Calls {
		cursor, err := OpenFunctionCall(c, n, i)
		if err != nil {
			s.Close()
			return FunctionSetInvocation{}, err
		}
		s.Cursors[i] = cursor
	}
	return s, nil
}

func OpenFunctionSource(c *Context, expression expr.Expr) (expr.SourceCursor, error) {
	if !expr.AdmitsSource(expression) {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	opening, ok := expression.(expr.SourceOpening)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	return opening.OpenSource(c)
}

func OpenFunctionFromNode(ctx context.Context, state *State, n *plan.FunctionFromNode) (Stage, error) {
	if n.Frame == 0 {
		return Stage{}, fmt.Errorf("stuntgres: invalid function source frame")
	}
	if err := ValidateFunctionFromLayout(n); err != nil {
		return Stage{}, err
	}
	source := &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host}
	EstablishSourcePresence(source, n.PresenceSlot)
	out := Stage{Source: source, Columns: n.Schema(), Selection: []int{}}
	var invocation FunctionSetInvocation
	opened := false
	ordinal := int64(0)
	frame := make([]sqltype.Value, len(n.Schema()))
	out.Finish = func() error { invocation.Close(); return nil }
	evaluation := state.Evaluation(nil)
	evaluation.Frame = n.Frame
	if state.Invocation != nil && state.Invocation.Scope == evaluation.Scope && state.Invocation.Frame == n.Frame {
		evaluation.Row = state.Invocation.Row
		evaluation.PresenceSlots = state.Invocation.PresenceSlots
		evaluation.Presence = state.Invocation.Presence
		evaluation.QueryOccurrences = state.Invocation.QueryOccurrences
		evaluation.QueryProvenance = state.Invocation.QueryProvenance
		evaluation.QueryCurrentRows = state.Invocation.QueryCurrentRows
	} else if n.Correlated {
		return Stage{}, fmt.Errorf("stuntgres: missing function invocation frame")
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		evaluation.setContext(ctx)
		if !opened {
			var err error
			invocation, err = OpenFunctionInvocation(&evaluation, n)
			if err != nil {
				return false, err
			}
			opened = true
		}
		present, err := invocation.NextFrom(ctx, n.Calls, frame)
		if err != nil {
			return false, err
		}
		if !present {
			invocation.Close()
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

func appendFunctionStageRow(ctx context.Context, out *Stage, schema []catalog.Column, row []sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source := out.Source
	if len(row) != source.FlatWidth || len(row) != len(schema) {
		return fmt.Errorf("stuntgres: invalid function stage width")
	}
	maxInt := int(^uint(0) >> 1)
	if source.FlatCount == maxInt || len(source.Flat) > maxInt-len(row) {
		return fmt.Errorf("stuntgres: function result is too large")
	}
	for i, v := range row {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := ValidateProducedValue(v, schema[i].Usage); err != nil {
			return err
		}
	}
	start := len(source.Flat)
	source.Flat = append(source.Flat, row...)
	if err := ctx.Err(); err != nil {
		source.Flat = source.Flat[:start]
		return err
	}
	out.Selection = append(out.Selection, source.FlatCount)
	source.FlatCount++
	return nil
}

func OpenSetProjectNode(ctx context.Context, state *State, n *plan.SetProjectNode) (Stage, error) {
	child, err := OpenNode(ctx, state, n.Child)
	if err != nil {
		return Stage{}, err
	}
	width := len(n.Schema())
	base, callWidth := n.BaseWidth, n.MarkerWidth
	if base < 0 || callWidth < len(n.Calls) || base+callWidth+len(n.Scalars) > width ||
		(len(n.Child.Schema()) != base && len(n.Child.Schema()) != width) || len(n.CallSlots) != len(n.Calls) || n.ScalarSlots != nil && len(n.ScalarSlots) != len(n.Scalars) {
		child.Close()
		return Stage{}, fmt.Errorf("stuntgres: invalid string set frame")
	}
	for i, slot := range n.CallSlots {
		if slot < 0 || slot >= callWidth {
			child.Close()
			return Stage{}, fmt.Errorf("stuntgres: invalid string set call slot")
		}
		if !SourceReturnsSet(n.Calls[i]) || n.Calls[i].ResultUsage() != n.Schema()[base+slot].Usage {
			child.Close()
			return Stage{}, fmt.Errorf("stuntgres: invalid function set call usage")
		}
		for _, previous := range n.CallSlots[:i] {
			if previous == slot {
				child.Close()
				return Stage{}, fmt.Errorf("stuntgres: duplicate string set call slot")
			}
		}
	}
	scalarWidth := width - base - callWidth
	for i := range n.Scalars {
		slot := QuerySetScalarSlot(n.ScalarSlots, i)
		if slot < 0 || slot >= scalarWidth || n.Scalars[i] == nil || QuerySetDepth(n.Scalars[i]) != 0 || n.Scalars[i].ResultUsage() != n.Schema()[base+callWidth+slot].Usage {
			child.Close()
			return Stage{}, fmt.Errorf("stuntgres: invalid string set scalar slot")
		}
		for previous := range i {
			if QuerySetScalarSlot(n.ScalarSlots, previous) == slot {
				child.Close()
				return Stage{}, fmt.Errorf("stuntgres: duplicate string set scalar slot")
			}
		}
	}
	if err := ValidateQuerySetSteps(n.Steps, len(n.Calls), len(n.Scalars)); err != nil {
		child.Close()
		return Stage{}, err
	}
	if n.Template != nil {
		if err := ValidateQuerySetSteps(n.Template.Steps, len(n.Template.Origins), len(n.Template.Scalars)); err != nil {
			child.Close()
			return Stage{}, err
		}
		if err := ValidateQuerySetStageOrder(n.Template.Steps, n.Steps, n.CallSlots, n.ScalarSlots, len(n.Scalars)); err != nil {
			child.Close()
			return Stage{}, err
		}
	}
	source := &Source{FlatWidth: width, Materialized: true, Authority: child.Source.Authority, Host: child.Source.Host, Occurrences: child.Source.Occurrences}
	out := Stage{Source: source, Columns: n.Schema(), Selection: []int{}, Pending: child.Pending, CacheWidth: child.CacheWidth}
	var invocation FunctionSetInvocation
	input := -1
	frame := make([]sqltype.Value, width)
	for i := range width - base {
		frame[base+i] = sqltype.Null(n.Schema()[base+i].Usage.Type)
	}
	evaluation := state.Evaluation(nil)
	evaluation.Frame = n.Frame
	out.Finish = func() error { invocation.Close(); return child.Close() }
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		for {
			evaluation.setContext(ctx)
			if input < 0 {
				i, present, err := child.NextOccurrence(ctx)
				if err != nil || !present {
					return present, err
				}
				input = i
				evaluation.BindQueryRow(child.Source, child.Index(i))
				evaluation.OrderValues = child.OrderRow(i)
				copy(frame, evaluation.Row)
				invocation = FunctionSetInvocation{Cursors: make([]expr.SourceCursor, len(n.Calls))}
			}
			present := len(n.Calls) == 0 // Zero-depth groups have one scalar attempt.
			for _, step := range n.Steps {
				if step.Kind == QuerySetScalarStep {
					value, err := n.Scalars[step.Index].Eval(&evaluation)
					if err != nil {
						return false, err
					}
					frame[base+callWidth+QuerySetScalarSlot(n.ScalarSlots, step.Index)] = value
					continue
				}
				call := n.Calls[step.Index]
				cursor := &invocation.Cursors[step.Index]
				if cursor.Type == nil {
					opened, err := OpenFunctionSource(&evaluation, call)
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
				invocation.Close()
				input = -1
				continue
			}
			if err := appendFunctionStageRow(ctx, out, n.Schema(), frame); err != nil {
				return false, err
			}
			if out.CacheWidth != 0 {
				out.OrderValues = append(out.OrderValues, child.OrderRow(input)...)
			}
			if len(source.Occurrences) != 0 {
				source.Provenance = AppendQueryProvenance(source.Provenance, child.Source, child.Index(input))
				if state.Source.Locks != nil {
					source.Versions = AppendQueryVersions(source.Versions, child.Source, child.Index(input), state.Source.Locks)
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
func FunctionFromCallWidth(call expr.Expr) int {
	if typ := call.ResultUsage().Type.BaseType(); typ.IsComposite() {
		return typ.CompositeLen()
	}
	return 1
}

func ValidateFunctionFromLayout(n *plan.FunctionFromNode) error {
	if len(n.Calls) == 0 {
		return fmt.Errorf("stuntgres: empty function source")
	}
	if n.ScalarNull != nil && len(n.ScalarNull) != len(n.Calls) {
		return fmt.Errorf("stuntgres: invalid function source scalar plan")
	}
	offset := 0
	for i, call := range n.Calls {
		if n.ScalarNull != nil && n.ScalarNull[i] {
			if !StrictScalarFunctionSource(call) {
				return fmt.Errorf("stuntgres: invalid function source scalar plan")
			}
		}
		width := FunctionFromCallWidth(call)
		if !SourceAdmitted(call) || width < 0 || offset+width > len(n.Schema()) {
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

func (s *FunctionSetInvocation) NextFrom(ctx context.Context, calls []expr.Expr, row []sqltype.Value) (bool, error) {
	present, offset := false, 0
	for i := range s.Cursors {
		typ := calls[i].ResultUsage().Type
		value, ok, err := s.Cursors[i].Next(ctx)
		if err != nil {
			return false, err
		}
		width := FunctionFromCallWidth(calls[i])
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
