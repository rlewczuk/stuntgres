package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// These are closed query stages, not a general table-function provider surface.
type functionFromNode struct {
	querySchema
	calls                  []expr.Expr
	scalarNull             []bool // Execution-only strict scalar plans; analyzed calls retain the stage layout.
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
	expressions    []expr.Expr
	origins        []expr.Expr
	scalars        []expr.Expr // Typed non-call owner candidates, before lower-marker substitution.
	scalarRequests []int       // Original owner request indices, not source-text identity.
	attempts       bool
	steps          []querySetStep // Original fixed marker/scalar identities in executable root order.
}

type setProjectNode struct {
	querySchema
	child                  LogicalNode
	calls                  []expr.Expr
	scalars                []expr.Expr
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

func preflightFunctionFrom(c *EvalContext, n *functionFromNode) (LogicalNode, error) {
	// Plan every child before simplifying a source call. Keep the analyzed
	// call carrier: it owns aliases, record expansion and the fixed stage width.
	planned, err := rewriteQueryExpressions(n, func(expr expr.Expr) (expr.Expr, bool, error) {
		return preflightCoercions(c, expr)
	})
	if err != nil {
		return nil, err
	}
	source := planned.(*functionFromNode)
	var scalarNull []bool
	for i, call := range source.calls {
		if !strictScalarFunctionSource(call) {
			continue
		}
		next, _, err := simplifyConditional(c, call, false)
		if err != nil {
			return nil, err
		}
		if randomLiteralNull(next) {
			if scalarNull == nil {
				scalarNull = make([]bool, len(source.calls))
			}
			scalarNull[i] = true
		}
	}
	if scalarNull == nil && source.scalarNull == nil {
		return source, nil
	}
	copy := *source
	copy.scalarNull = scalarNull
	return &copy, nil
}

func (n *functionFromNode) openCall(c *EvalContext, i int) (expr.SourceCursor, error) {
	if n.scalarNull != nil && n.scalarNull[i] {
		if err := evaluationContext(c); err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(sqltype.Null(n.calls[i].ResultUsage().Type)), nil
	}
	return openFunctionSource(c, n.calls[i])
}

func openFunctionInvocation(c *EvalContext, n *functionFromNode) (functionSetInvocation, error) {
	s := functionSetInvocation{cursors: make([]expr.SourceCursor, len(n.calls))}
	for i := range n.calls {
		cursor, err := n.openCall(c, i)
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

func openFunctionFromNode(ctx context.Context, state *queryState, n *functionFromNode) (indexedStage, error) {
	if n.frame == 0 {
		return indexedStage{}, fmt.Errorf("stuntgres: invalid function source frame")
	}
	if err := validateFunctionFromLayout(n); err != nil {
		return indexedStage{}, err
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
			invocation, err = openFunctionInvocation(&evaluation, n)
			if err != nil {
				return false, err
			}
			opened = true
		}
		present, err := invocation.nextFrom(ctx, n.calls, frame)
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
		if !sourceReturnsSet(n.calls[i]) || n.calls[i].ResultUsage() != n.Schema()[base+slot].Usage {
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
				invocation = functionSetInvocation{cursors: make([]expr.SourceCursor, len(n.calls))}
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

// Result usages are the immutable source of FROM spans. Targets continue to
// occupy one record-valued marker slot; only FROM expands member columns.
func functionFromCallWidth(call expr.Expr) int {
	if typ := call.ResultUsage().Type.BaseType(); typ.IsComposite() {
		return typ.CompositeLen()
	}
	return 1
}

func validateFunctionFromLayout(n *functionFromNode) error {
	if len(n.calls) == 0 {
		return fmt.Errorf("stuntgres: empty function source")
	}
	if n.scalarNull != nil && len(n.scalarNull) != len(n.calls) {
		return fmt.Errorf("stuntgres: invalid function source scalar plan")
	}
	offset := 0
	for i, call := range n.calls {
		if n.scalarNull != nil && n.scalarNull[i] {
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
	if n.ordinality {
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
