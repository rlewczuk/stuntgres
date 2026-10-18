package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
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

type functionCursorKind uint8

const (
	functionStringCursor functionCursorKind = iota
	functionOwnedSetCursor
	functionScalarCursor
	functionJSONKeysCursor
	functionJSONEachCursor
	functionJSONElementsCursor
	functionJSONBElementsCursor
	functionSeriesCursor
	functionMultirangeCursor
	functionArrayCursor
	functionNumericSeriesCursor
	functionJSONRecordCursor
	functionACLCursor
	functionCatalogKeysCursor
)

type functionSetCursor struct {
	cursor                 sqltype.StringFieldCursor
	keys                   sqltype.JSONKeyCursor
	each                   sqltype.JSONEachCursor
	record                 sqltype.JSONRecordCursor
	binaryElements         sqltype.JSONBElementCursor
	members                sqltype.MultirangeCursor
	elements               sqltype.JSONElementsCursor
	numericSeries          sqltype.NumericSeriesCursor
	array                  sqltype.Value
	owned                  []sqltype.Value
	acl                    aclExplodeCursor
	catalogKeys            *catalogForeignKeyValues
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
	if s.initialized && s.kind == functionJSONEachCursor {
		s.each.Close()
	}
	if s.initialized && s.kind == functionJSONBElementsCursor {
		s.binaryElements.Close()
	}
	if s.initialized && s.kind == functionMultirangeCursor {
		s.members.Close()
	}
	if s.initialized && s.kind == functionJSONElementsCursor {
		s.elements.Close()
	}
	if s.initialized && s.kind == functionNumericSeriesCursor {
		s.numericSeries.Close()
	}
	if s.initialized && s.kind == functionJSONRecordCursor {
		s.record.Close()
	}
	if s.initialized && s.kind == functionACLCursor {
		s.acl.close()
	}
	s.owned = nil
	s.catalogKeys = nil
	s.array = sqltype.Value{}
	s.exhausted = true
}
func (s *functionSetInvocation) close() {
	for i := range s.cursors {
		s.cursors[i].close()
	}
	s.cursors = nil
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
	for i, expr := range source.calls {
		call, ok := expr.(objectAddressExpression)
		if !ok || call.op != describeObject {
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

func (n *functionFromNode) openCall(c *EvalContext, i int) (functionSetCursor, error) {
	if n.scalarNull != nil && n.scalarNull[i] {
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: sqltype.Null(n.calls[i].ResultUsage().Type), kind: functionScalarCursor, initialized: true}, nil
	}
	return openFunctionCall(c, n.calls[i])
}

func openFunctionInvocation(c *EvalContext, n *functionFromNode) (functionSetInvocation, error) {
	s := functionSetInvocation{cursors: make([]functionSetCursor, len(n.calls))}
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
func openFunctionCall(c *EvalContext, expression expr.Expr) (functionSetCursor, error) {
	switch call := expression.(type) {
	case functions.JSONBPathQuery:
		values, err := call.EvalSet(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{owned: values, kind: functionOwnedSetCursor, initialized: true}, nil
	case catalogUserNameExpression:
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case catalogForeignKeysExpression:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if err := call.validate(); err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{catalogKeys: call.metadata, kind: functionCatalogKeysCursor, initialized: true}, nil
	case transactionIdentityExpression:
		if call.kind == identityMembers {
			rows, err := call.openMembers(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			return functionSetCursor{owned: rows, kind: functionOwnedSetCursor, initialized: true}, nil
		}
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case aclValueExpression:
		if call.kind == aclExplode {
			cursor, err := call.openACL(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			return functionSetCursor{acl: cursor, kind: functionACLCursor, initialized: true}, nil
		}
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case functions.JSONRecord:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if !call.Plan.ReturnsSet() {
			value, err := call.Eval(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
		}
		var values [2]sqltype.Value
		for i, arg := range call.Args {
			value, err := arg.Eval(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			values[i] = value
		}
		cursor, err := call.Plan.Open(c.Context, values[:len(call.Args)], c.FunctionInput())
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{record: cursor, kind: functionJSONRecordCursor, initialized: true}, nil
	case functions.JSONBRecord:
		if call.ReturnsSet() {
			values, err := call.EvalSet(c)
			if err != nil {
				return functionSetCursor{}, err
			}
			return functionSetCursor{owned: values, kind: functionOwnedSetCursor, initialized: true}, nil
		}
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case seriesExpression:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.args) != 2 && len(call.args) != 3 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid series arguments")
		}
		if call.ResultUsage().Type.ID() == sqltype.Numeric {
			var values [3]sqltype.Value
			for i, arg := range call.args {
				value, err := arg.Eval(c)
				if err != nil {
					return functionSetCursor{}, err
				}
				values[i] = value
			}
			cursor, err := sqltype.OpenNumericSeries(c.Context, values[0], values[1], values[2:len(call.args)]...)
			if err != nil {
				return functionSetCursor{}, err
			}
			return functionSetCursor{kind: functionNumericSeriesCursor, initialized: true, numericSeries: cursor}, nil
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
	case unnestExpression:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		value, err := call.args[0].Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		if input := call.args[0].ResultUsage().Type; input.BaseType().IsArray() {
			if value.Type() != input || input.BaseType().Element() != call.ResultUsage().Type {
				return functionSetCursor{}, fmt.Errorf("stuntgres: invalid array unnest generation")
			}
			return functionSetCursor{array: value, kind: functionArrayCursor, initialized: true, exhausted: value.IsNull() || value.ArrayLen() == 0}, nil
		}
		cursor, err := sqltype.OpenMultirange(c.Context, call.args[0].ResultUsage(), call.ResultUsage(), value)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{members: cursor, kind: functionMultirangeCursor, initialized: true}, nil
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
	case functions.JSONKeys:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.Args) != 1 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid JSON keys arguments")
		}
		value, err := call.Args[0].Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		cursor, err := call.Plan.Open(c.Context, value)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{keys: cursor, kind: functionJSONKeysCursor, initialized: true}, nil
	case functions.JSONBElements:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.Args) != 1 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid JSONB elements arguments")
		}
		value, err := call.Args[0].Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		cursor, err := call.Plan.Open(c.Context, value)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{binaryElements: cursor, kind: functionJSONBElementsCursor, initialized: true}, nil
	case functions.JSONEach:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.Args) != 1 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid JSON each arguments")
		}
		value, err := call.Args[0].Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		cursor, err := call.Plan.Open(c.Context, value)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{each: cursor, kind: functionJSONEachCursor, initialized: true}, nil
	case objectAddressExpression:
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case commitTimestampExpression:
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case functions.RangeInspect:
		if !validFunctionSourceCall(call) {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
		}
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case functions.JSONElements:
		if err := evaluationContext(c); err != nil {
			return functionSetCursor{}, err
		}
		if len(call.Args) != 1 {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid JSON elements arguments")
		}
		value, err := call.Args[0].Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		cursor, err := call.Plan.Open(c.Context, value)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{elements: cursor, kind: functionJSONElementsCursor, initialized: true}, nil
	case functions.Binary:
		if !validFunctionSourceCall(call) {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
		}
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case functions.RegexpSet:
		values, err := call.EvalSet(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{owned: values, kind: functionOwnedSetCursor, initialized: true}, nil
	case functions.Regexp:
		value, err := call.Eval(c)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{scalar: value, kind: functionScalarCursor, initialized: true}, nil
	case functions.String:
		if !isStringSet(call) {
			return functionSetCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
		}
		var values [4]sqltype.Value
		for j, arg := range call.Args {
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
		output.Regcollation, output.Regnamespace = scope, scope
		output.Regproc = scope
		output.Operator = scope
		output.RegTS = scope
		cursor, err := call.Plan.OpenStringTable(c.Context, values, c.Input, output)
		if err != nil {
			return functionSetCursor{}, err
		}
		return functionSetCursor{cursor: cursor, initialized: true}, nil
	default:
		return functionSetCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
}
func (s *functionSetInvocation) next(ctx context.Context, calls []expr.Expr, row []sqltype.Value, slots []int) (bool, error) {
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
	if s.kind == functionACLCursor {
		return s.acl.next(ctx, typ)
	}
	if s.kind == functionCatalogKeysCursor {
		return s.nextCatalogForeignKey(typ)
	}
	if s.kind == functionArrayCursor {
		if s.position == s.array.ArrayLen() {
			s.close()
			return sqltype.Null(typ), false, nil
		}
		value := s.array.ArrayElement(s.position)
		if value.Type() != typ {
			return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid array unnest element generation")
		}
		s.position++
		return value, true, nil
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
		s.close()
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
	if s.kind == functionNumericSeriesCursor {
		value, present, err = s.numericSeries.Next(ctx)
	} else if s.kind == functionJSONKeysCursor {
		value, present, err = s.keys.Next(ctx)
	} else if s.kind == functionJSONEachCursor {
		value, present, err = s.each.Next(ctx)
	} else if s.kind == functionJSONRecordCursor {
		value, present, err = s.record.Next(ctx)
	} else if s.kind == functionJSONBElementsCursor {
		value, present, err = s.binaryElements.Next(ctx)
	} else if s.kind == functionMultirangeCursor {
		value, present, err = s.members.Next(ctx)
	} else if s.kind == functionJSONElementsCursor {
		value, present, err = s.elements.Next(ctx)
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
			if state.functionRecheck != nil {
				invocation, err = state.functionRecheck.cache.open(&evaluation, n)
			} else {
				invocation, err = openFunctionInvocation(&evaluation, n)
			}
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
			scalar, ok := call.(objectAddressExpression)
			if !ok || scalar.op != describeObject || call.ResultUsage().Type.ID() != sqltype.Text {
				return fmt.Errorf("stuntgres: invalid function source scalar plan")
			}
		}
		width := functionFromCallWidth(call)
		if !validFunctionSourceCall(call) || width < 0 || offset+width > len(n.Schema()) {
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
		value, ok, err := s.cursors[i].next(ctx, typ)
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
