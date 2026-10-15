package engine

import (
	"fmt"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Window occurrences reference immutable appended stage slots, never aggregate state.
type windowExpression struct {
	expressionInfo
	slot     int
	frame    uint32
	call     int
	nullable bool
}

func (e *windowExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.slot < 0 || e.slot >= len(c.Row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing window output")
	}
	return c.Row[e.slot], nil
}

type boundWindowSpec struct {
	partition  []BoundExpr
	grouping   []sqltype.Grouping
	order      []sortKey
	orderMap   []int // Raw-to-effective mapping, for literal capture only.
	frame      boundWindowFrame
	explicit   bool
	definition int // Query-local named ordinal+1; zero retains inline spec identity.
}
type windowOperation uint8

const (
	windowCount windowOperation = iota
	windowNumber
	windowSum
	windowFirst
	windowLast
	windowNth
	windowNtile
	windowLead
	windowLag
	windowRank
	windowDenseRank
	windowPercentRank
	windowCumeDist
)

func (operation windowOperation) frameDependent() bool {
	return operation != windowNumber && operation != windowNtile && operation != windowLead && operation != windowLag && !operation.ranking()
}

func (operation windowOperation) evaluatesArgument() bool {
	return operation == windowFirst || operation == windowLast || operation == windowNth || operation == windowNtile || operation == windowLead || operation == windowLag
}

func (operation windowOperation) ranking() bool {
	return operation >= windowRank && operation <= windowCumeDist
}

type windowCall struct {
	expression   *windowExpression
	spec         boundWindowSpec
	operation    windowOperation
	argument     BoundExpr
	ordinal      BoundExpr
	defaultValue BoundExpr
	provider     aggregateProvider
	raw          *parser.WindowSpec
	analysisOnly bool // Matched ORDER target: retain binding ownership, not execution.
}

type boundWindowBound struct {
	kind    parser.FrameBoundKind
	offset  BoundExpr
	support sqltype.RangeSupport
}
type boundWindowFrame struct {
	unit       parser.FrameUnit
	exclusion  parser.FrameExclusion
	start, end boundWindowBound
}

// A same-query call owns only its own argument occurrences. Evidence is checked
// after that call resolves, so an invalid signature is never hidden by nesting.
type argumentWindowOwner struct {
	active, window, found bool
	first                 parser.Position
}

func (owner argumentWindowOwner) nestingError() error {
	if !owner.found {
		return nil
	}
	if owner.window {
		return expressionError("42P20", "window function calls cannot be nested", owner.first)
	}
	return expressionError("42803", "aggregate function calls cannot contain window function calls", owner.first)
}

func (b *expressionBinder) windowAllowed(pos parser.Position) error {
	if b.policyConsumer && b.queryFrame == 0 {
		return expressionError("42P20", "window functions are not allowed in policy expressions", pos)
	}
	if b.partitionConsumer != "" {
		return b.partitionExpressionError("42P20", "window functions are not allowed in "+b.partitionContextPlural(), pos)
	}
	clause := b.aggregateClause
	if b.checkConsumer {
		clause = "check constraints"
	} else if b.indexPredicateConsumer {
		clause = "index predicates"
	} else if b.indexConsumer {
		clause = "index expressions"
	} else if b.defaultConsumer {
		clause = "DEFAULT expressions"
	}
	if clause == "window definitions" || clause == "window RANGE" || clause == "window GROUPS" || clause == "window ROWS" {
		return expressionError("42P20", "window functions are not allowed in window definitions", pos)
	}
	if clause != "SELECT" && clause != "ORDER BY" && clause != "DISTINCT ON" {
		return expressionError("42P20", "window functions are not allowed in "+clause, pos)
	}
	return nil
}
func (b *expressionBinder) bindWindow(raw parser.AggregateExpr, name parser.ObjectName) (boundResult BoundExpr, bindError error) {
	parentOwner := b.argumentOwner
	parentAggregates, parentWindows := b.aggregates, b.windowCalls
	defer func() {
		if bindError != nil {
			b.aggregates, b.windowCalls = parentAggregates, parentWindows
		}
	}()
	b.argumentOwner = argumentWindowOwner{active: true, window: true}
	defer func() { b.argumentOwner = parentOwner }()
	resultUsage := b.typ(sqltype.Int8).Usage()
	var argument, ordinal, defaultValue BoundExpr
	if windowOffsetName(raw.Name) {
		var err error
		argument, ordinal, defaultValue, resultUsage, err = b.resolveWindowOffsetSignature(raw, name)
		if err != nil {
			return nil, err
		}
	} else if windowValueName(raw.Name) {
		var err error
		argument, ordinal, resultUsage, err = b.resolveWindowValueSignature(raw, name)
		if err != nil {
			return nil, err
		}
	} else if raw.Name == "ntile" {
		var err error
		argument, err = b.resolveNtileSignature(raw, name)
		if err != nil {
			return nil, err
		}
		resultUsage = b.typ(sqltype.Int4).Usage()
	} else if hypotheticalSetName(raw.Name) {
		if _, err := b.bindAggregateFilter(raw); err != nil {
			return nil, err
		}
		visible, err := b.builtinVisible(name, functionLookup)
		if err != nil {
			return nil, err
		}
		if !visible {
			return nil, functionSignatureErrorForTypes(name, raw.Pos, nil)
		}
		if raw.Name == "percent_rank" || raw.Name == "cume_dist" {
			resultUsage = b.typ(sqltype.Float8).Usage()
		}
	} else if raw.Name == "count" || raw.Name == "row_number" {
		args := make([]BoundExpr, len(raw.Args))
		for i, arg := range raw.Args {
			var err error
			args[i], err = b.bind(arg, expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
		}
		if _, err := b.bindAggregateFilter(raw); err != nil {
			return nil, err
		}
		if raw.Name == "count" {
			if err := b.resolveCountSignature(raw, name, args); err != nil {
				return nil, err
			}
		} else {
			types := make([]string, len(args))
			for i, arg := range args {
				types[i] = functionArgumentTypeName(raw.Args[i], arg)
			}
			if err := b.resolveRowNumberSignature(raw, name, types); err != nil {
				return nil, err
			}
		}
	} else {
		var err error
		resultUsage, err = b.resolveWindowAggregateSignature(raw, name, &argument)
		if err != nil {
			return nil, err
		}
	}
	if raw.Distinct {
		return nil, expressionError("0A000", "DISTINCT is not implemented for window functions", raw.Pos)
	}
	if len(raw.Order) > 0 {
		return nil, expressionError("0A000", "aggregate ORDER BY is not implemented for window functions", raw.Pos)
	}
	if raw.Filter != nil && (raw.Name == "row_number" || raw.Name == "ntile" || hypotheticalSetName(raw.Name) || windowValueName(raw.Name) || windowOffsetName(raw.Name)) {
		return nil, expressionError("0A000", "FILTER is not implemented for non-aggregate window functions", raw.Pos)
	}
	if err := b.windowAllowed(raw.Pos); err != nil {
		return nil, err
	}
	if err := b.argumentOwner.nestingError(); err != nil {
		return nil, err
	}
	if raw.Window.Name != nil && raw.Filter == nil && (raw.Name == "row_number" || raw.Name == "ntile" || raw.Name == "sum" || hypotheticalSetName(raw.Name) || windowValueName(raw.Name) || windowOffsetName(raw.Name) || (raw.Name == "count" && raw.Star)) {
		found := false
		for _, definition := range b.windowRaw {
			if definition.Name.Text == raw.Window.Name.Text {
				found = true
				break
			}
		}
		if !found {
			return nil, expressionError("42704", fmt.Sprintf(`window "%s" does not exist`, raw.Window.Name.Text), raw.Window.Pos)
		}
	}
	if parentOwner.active {
		if !parentOwner.found {
			parentOwner.first, parentOwner.found = raw.Pos, true
		}
		// Analysis-only occurrence: the completed parent must either reject its
		// signature or report nesting. It cannot enter an executable window plan.
		return &windowExpression{expressionInfo: b.info(resultUsage, raw.Pos, ExprProperties{StageDependent: true}), slot: -1, call: -1, frame: b.frameID()}, nil
	}
	if raw.Filter != nil {
		return nil, expressionError("0A000", "window aggregate FILTER is unsupported", raw.FilterPos)
	}
	if raw.Name != "sum" && raw.Name != "row_number" && raw.Name != "ntile" && !hypotheticalSetName(raw.Name) && !windowValueName(raw.Name) && !windowOffsetName(raw.Name) && (raw.Name != "count" || !raw.Star) {
		return nil, expressionError("0A000", "window function provider is unsupported", raw.Pos)
	}
	call := windowCall{raw: raw.Window, argument: argument, ordinal: ordinal, defaultValue: defaultValue}
	switch raw.Name {
	case "row_number":
		call.operation = windowNumber
	case "rank":
		call.operation = windowRank
	case "dense_rank":
		call.operation = windowDenseRank
	case "percent_rank":
		call.operation = windowPercentRank
	case "cume_dist":
		call.operation = windowCumeDist
	case "ntile":
		call.operation = windowNtile
	case "lead":
		call.operation = windowLead
	case "lag":
		call.operation = windowLag
	case "first_value":
		call.operation = windowFirst
	case "last_value":
		call.operation = windowLast
	case "nth_value":
		call.operation = windowNth
	case "sum":
		call.operation = windowSum
		// Ordinary SUM has additional providers; windows remain numeric-only.
		switch argument.ResultUsage().Type.ID() {
		case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8:
		default:
			return nil, expressionError("0A000", "window function provider is unsupported", raw.Pos)
		}
		var err error
		call.provider, _, err = b.sumProvider(argument.ResultUsage().Type)
		if err != nil {
			return nil, err
		}
	}
	info := b.info(resultUsage, raw.Pos, ExprProperties{StageDependent: true})
	if raw.Name == "ntile" {
		info.properties = mergeExpressionProperties(info.properties, argument.Properties())
	}
	if windowValueName(raw.Name) || windowOffsetName(raw.Name) {
		info.properties = mergeExpressionProperties(info.properties, argument.Properties())
		if ordinal != nil {
			info.properties = mergeExpressionProperties(info.properties, ordinal.Properties())
		}
		var err error
		if defaultValue != nil {
			info.properties = mergeExpressionProperties(info.properties, defaultValue.Properties())
			info, err = b.collatedInfo(info, argument, defaultValue)
		} else {
			info, err = b.collatedInfo(info, argument)
		}
		if err != nil {
			return nil, err
		}
	}
	expr := &windowExpression{expressionInfo: info, slot: -1, frame: b.frameID(), call: len(b.windowCalls)}
	call.expression = expr
	expr.nullable = call.operation == windowSum || call.operation == windowNtile || windowValueName(raw.Name) || windowOffsetName(raw.Name)
	b.windowCalls = append(b.windowCalls, call)
	return expr, nil
}

func (b *expressionBinder) resolveRowNumberSignature(raw parser.AggregateExpr, name parser.ObjectName, types []string) error {
	if len(raw.Args) > 100 {
		return expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return err
	}
	if !visible || len(raw.Args) != 0 || raw.Star {
		return functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	return nil
}

// The excluded-provider path performs the same signature/coercion analysis as
// ordinary aggregation, but never builds a provider or registers an aggregate.
// In particular, raw call-local ORDER expressions are not transformed here.
func (b *expressionBinder) resolveWindowAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, argument *BoundExpr) (sqltype.Usage, error) {
	if raw.Name == "json_objectagg" && raw.JSON != nil {
		_, usage, returning, err := b.resolveSQLJSONObjectAggregate(raw, name)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if _, err = b.bindAggregateFilter(raw); err != nil {
			return sqltype.Usage{}, err
		}
		if returning.Type != nil {
			_, err = jsonReturningCoercion(usage, returning, raw.JSON.Output)
			usage = returning
		}
		return usage, err
	}
	var small [2]comparisonOperand
	var names [2]string
	var operands []comparisonOperand
	var types []string
	if len(raw.Args) <= 2 {
		operands, types = small[:len(raw.Args)], names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.comparisonOperand(child)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if operands != nil {
			operands[i] = operand
			types[i] = "unknown"
			if raw.Name == "sum" || raw.Name == "array_agg" || raw.Name == "bit_and" || raw.Name == "bit_or" || raw.Name == "bit_xor" {
				types[i] = b.diagnosticTypeName(operand.descriptor())
			} else if typ := operand.descriptor(); typ != nil {
				types[i] = typ.DiagnosticName()
			}
			if raw.Name == "min" || raw.Name == "max" || raw.Name == "range_agg" || raw.Name == "range_intersect_agg" {
				types[i], err = b.extremaArgumentTypeName(operand)
				if err != nil {
					return sqltype.Usage{}, err
				}
			}
		}
	}
	if _, err := b.bindAggregateFilter(raw); err != nil {
		return sqltype.Usage{}, err
	}
	if len(raw.Args) > 100 {
		return sqltype.Usage{}, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	var operand comparisonOperand
	if len(operands) > 0 {
		operand = operands[0]
	}
	var arg BoundExpr
	var err error
	var usage sqltype.Usage
	switch raw.Name {
	case "bool_and", "bool_or", "every":
		arg, err = b.resolveBooleanAggregateSignature(raw, name, operands, types)
		if err == nil {
			usage = arg.ResultUsage()
		}
	case "avg":
		var operation aggregateOperation
		arg, operation, err = b.resolveAvgSignature(raw, name, operand, types)
		if err == nil {
			switch operation {
			case aggregateIntervalAvg:
				usage = arg.ResultUsage()
			case aggregateNumericAvg:
				usage = b.typ(sqltype.Numeric).Usage()
			case aggregateFloatAvg:
				usage = b.typ(sqltype.Float8).Usage()
			}
		}
	case "var_pop", "var_samp", "variance", "stddev_pop", "stddev_samp", "stddev", "regr_slope", "regr_sxx", "regr_sxy", "regr_syy",
		"corr", "covar_pop", "covar_samp", "regr_avgx", "regr_avgy", "regr_count", "regr_intercept", "regr_r2":
		var args [2]BoundExpr
		args, usage, err = b.resolveStatisticalSignature(raw, name, operands, types)
		arg = args[0]
	case "any_value":
		arg, err = b.resolveAnyValueSignature(raw, name, operand.node, sqltype.Usage{Type: operand.descriptor()}, types)
		if err == nil {
			usage = arg.ResultUsage()
			usage.Typmod = -1
		}
	case "json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict", "json_arrayagg":
		var returning sqltype.Usage
		arg, usage, returning, err = b.resolveJSONArrayAggregateSignature(raw, name, operand, types)
		if err == nil && returning.Type != nil {
			_, err = jsonReturningCoercion(usage, returning, raw.JSON.Output)
			usage = returning
		}
	case "json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict":
		var args [2]BoundExpr
		args, usage, err = b.resolveJSONObjectAggregateSignature(raw, name, operands, types)
		arg = args[0]
	case "min", "max":
		arg, err = b.resolveExtremaSignature(raw, name, operand, types)
		if err == nil {
			usage = arg.ResultUsage()
			usage.Typmod = -1
		}
	case "string_agg":
		arg, _, err = b.resolveStringAggregateSignature(raw, name, operands, types)
		if err == nil {
			usage = arg.ResultUsage()
		}
	case "array_agg":
		arg, usage, _, err = b.resolveArrayAggregateSignature(raw, name, operand, types)
	case "range_agg", "range_intersect_agg":
		arg, err = b.resolveRangeAggregateSignature(raw, name, operand, types)
		if err == nil {
			operation := sqltype.RangeAggregateUnion
			if raw.Name == "range_intersect_agg" {
				operation = sqltype.RangeAggregateIntersection
			}
			var bound sqltype.RangeAggregate
			bound, err = sqltype.BindRangeAggregate(arg.ResultUsage().Type, operation)
			if err == nil {
				usage = bound.ResultUsage()
			}
		}
	case "bit_and", "bit_or", "bit_xor":
		arg, err = b.resolveBitwiseAggregateSignature(raw, name, operand, types)
		if err == nil {
			usage = arg.ResultUsage().Type.Usage()
		}
	case "sum":
		arg, err = b.resolveSumSignature(raw, name, operand, types)
		if err == nil {
			switch arg.ResultUsage().Type.ID() {
			case sqltype.Int2, sqltype.Int4:
				usage = b.typ(sqltype.Int8).Usage()
			case sqltype.Int8, sqltype.Numeric:
				usage = b.typ(sqltype.Numeric).Usage()
			default:
				usage = arg.ResultUsage()
			}
		}
	default:
		err = functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	*argument = arg
	return usage, err
}

// Resolve keys only after target, predicate, grouping and outer order analysis.
func (b *expressionBinder) resolveWindows() error {
	if err := b.resolveWindowDefinitions(); err != nil {
		return err
	}
	for i := range b.windowCalls {
		spec, err := b.bindWindowSpec(*b.windowCalls[i].raw, b.windowDefinitions, true)
		if err != nil {
			return err
		}
		b.windowCalls[i].spec = spec
		b.windowCalls[i].raw = nil
	}
	return nil
}

func (b *expressionBinder) sameWindowCall(a, c windowCall) bool {
	if a.operation != c.operation {
		return false
	}
	// Arguments can own child queries hidden by the window leaf. This is
	// semantic target equality, not permission to merge volatile executions.
	if (a.operation == windowSum || a.operation.evaluatesArgument()) && (!sameRecordUsage(a.expression.ResultUsage(), c.expression.ResultUsage()) || !b.sameQueryTarget(a.argument, c.argument) || !b.sameQueryTarget(a.ordinal, c.ordinal) || !b.sameQueryTarget(a.defaultValue, c.defaultValue)) {
		return false
	}
	if a.raw != nil || c.raw != nil {
		return sameRawWindow(reflect.ValueOf(a.raw), reflect.ValueOf(c.raw))
	}
	return b.sameWindowSpec(a.spec, c.spec)
}

// PostgreSQL assigns raw window references before transforming their keys.
// Compare those trees without source locations; this is binding-only and does
// not allocate copies or leak unresolved keys into the executable plan.
func sameRawWindow(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}
	if a.Type() == reflect.TypeOf(parser.Position{}) {
		return true
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return sameRawWindow(a.Elem(), b.Elem())
	case reflect.Struct:
		for i := range a.NumField() {
			if !sameRawWindow(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if a.Len() != b.Len() {
			return false
		}
		for i := range a.Len() {
			if !sameRawWindow(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.String:
		return a.String() == b.String()
	case reflect.Bool:
		return a.Bool() == b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return a.Uint() == b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() == b.Float()
	}
	return false
}
func (b *expressionBinder) resolveWindowDefinitions() error {
	if b.windowDefinitions != nil {
		return nil
	}
	if len(b.windowRaw) == 0 {
		return nil
	}
	b.windowDefinitions = make(map[string]boundWindowSpec, len(b.windowRaw))
	for definitionIndex, definition := range b.windowRaw {
		if _, ok := b.windowDefinitions[definition.Name.Text]; ok {
			return expressionError("42P20", fmt.Sprintf(`window "%s" is already defined`, definition.Name.Text), definition.Spec.Pos)
		}
		spec, err := b.bindWindowSpec(definition.Spec, b.windowDefinitions, false)
		if err != nil {
			return err
		}
		spec.definition = definitionIndex + 1
		if b.noParameters || b.materializedDefinition {
			ordinal := 0
			path := viewLiteralPath{query: b.frameID(), clause: viewWindowLiteral, target: definitionIndex}
			for i, key := range definition.Spec.Partition {
				if err := b.captureViewLiteralTree(key, spec.partition[i], path, &ordinal); err != nil {
					return err
				}
			}
			for i, key := range definition.Spec.Order {
				if err := b.captureViewLiteralTree(key.Expr, spec.order[spec.orderMap[i]].expression, path, &ordinal); err != nil {
					return err
				}
			}
			if definition.Spec.Frame != nil {
				if err := b.captureViewLiteralTree(definition.Spec.Frame.Start.Offset, spec.frame.start.offset, path, &ordinal); err != nil {
					return err
				}
				if err := b.captureViewLiteralTree(definition.Spec.Frame.End.Offset, spec.frame.end.offset, path, &ordinal); err != nil {
					return err
				}
			}
		}
		b.windowDefinitions[definition.Name.Text] = spec
	}
	return nil
}
func (b *expressionBinder) bindWindowSpec(raw parser.WindowSpec, names map[string]boundWindowSpec, overClause bool) (boundWindowSpec, error) {
	var parent boundWindowSpec
	if raw.Name != nil {
		var ok bool
		parent, ok = names[raw.Name.Text]
		if !ok {
			return boundWindowSpec{}, expressionError("42704", fmt.Sprintf(`window "%s" does not exist`, raw.Name.Text), raw.Pos)
		}
		if raw.Bare {
			return parent, nil
		}
	}
	old := b.aggregateClause
	b.aggregateClause = "window definitions"
	defer func() { b.aggregateClause = old }()
	spec := boundWindowSpec{order: make([]sortKey, 0, len(raw.Order)), orderMap: make([]int, len(raw.Order)), partition: make([]BoundExpr, len(raw.Partition)), grouping: make([]sqltype.Grouping, len(raw.Partition)), explicit: raw.Frame != nil}
	spec.frame.start.kind, spec.frame.end.kind = parser.FrameUnboundedPreceding, parser.FrameCurrentRow
	for i, key := range raw.Order {
		expr, err := b.bind(key.Expr, expressionPolicy{context: projectionContext})
		if err != nil {
			return spec, err
		}
		expr, err = b.finalizeProjection(expr)
		if err != nil {
			return spec, err
		}
		if expr.CollationIndeterminate() {
			_, err := b.bindDistinctGrouping(expr)
			return spec, err
		}
		comparison, err := sqltype.BindOrdering(expr.ResultUsage())
		if d, ok := err.(*sqlstate.Error); ok {
			copy := *d
			copy.Position = fmt.Sprint(key.Pos.Character)
			return spec, &copy
		}
		if err != nil {
			return spec, err
		}
		index := -1
		for j, previous := range spec.order {
			if b.sameGroupExpression(previous.expression, expr) {
				index = j
				break
			}
		}
		if index < 0 {
			index = len(spec.order)
			spec.order = append(spec.order, sortKey{expression: expr, comparison: comparison, position: key.Pos.Character, desc: key.Desc, nullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc})
		}
		spec.orderMap[i] = index
	}
	for i, key := range raw.Partition {
		expr, err := b.bind(key, expressionPolicy{context: projectionContext})
		if err != nil {
			return spec, err
		}
		expr, err = b.finalizeProjection(expr)
		if err != nil {
			return spec, err
		}
		grouping, err := b.bindDistinctGrouping(expr)
		if err != nil {
			return spec, err
		}
		spec.partition[i], spec.grouping[i] = expr, grouping
	}
	if raw.Name != nil {
		if len(raw.Partition) > 0 {
			return spec, expressionError("42P20", fmt.Sprintf(`cannot override PARTITION BY clause of window "%s"`, raw.Name.Text), raw.Pos)
		}
		if len(parent.order) > 0 && len(raw.Order) > 0 {
			return spec, expressionError("42P20", fmt.Sprintf(`cannot override ORDER BY clause of window "%s"`, raw.Name.Text), raw.Pos)
		}
		if parent.explicit {
			d := expressionError("42P20", fmt.Sprintf(`cannot copy window "%s" because it has a frame clause`, raw.Name.Text), raw.Pos).(*sqlstate.Error)
			if overClause && len(raw.Partition) == 0 && len(raw.Order) == 0 && raw.Frame == nil {
				d.Hint = "Omit the parentheses in this OVER clause."
			}
			return spec, d
		}
		spec.partition, spec.grouping = parent.partition, parent.grouping
		if len(spec.order) == 0 {
			spec.order = parent.order
		}
	}
	if raw.Frame != nil {
		spec.frame.unit = raw.Frame.Unit
		spec.frame.exclusion = raw.Frame.Exclusion
		if raw.Frame.Unit == parser.FrameGroups && len(spec.order) == 0 {
			return spec, expressionError("42P20", "GROUPS mode requires an ORDER BY clause", raw.Pos)
		}
		if raw.Frame.Unit == parser.FrameRange && (raw.Frame.Start.Offset != nil || raw.Frame.End.Offset != nil) && len(spec.order) != 1 {
			return spec, expressionError("42P20", "RANGE with offset PRECEDING/FOLLOWING requires exactly one ORDER BY column", raw.Pos)
		}
		var err error
		spec.frame.start, err = b.bindWindowBound(raw.Frame.Start, spec.order, raw.Frame.Unit)
		if err != nil {
			return spec, err
		}
		spec.frame.end, err = b.bindWindowBound(raw.Frame.End, spec.order, raw.Frame.Unit)
		if err != nil {
			return spec, err
		}
	}
	return spec, nil
}
func (b *expressionBinder) sameWindowSpec(a, c boundWindowSpec) bool {
	if a.definition != c.definition || a.frame.unit != c.frame.unit || a.frame.exclusion != c.frame.exclusion || !b.sameWindowSort(a, c) || a.explicit != c.explicit {
		return false
	}
	for _, pair := range [2][2]boundWindowBound{{a.frame.start, c.frame.start}, {a.frame.end, c.frame.end}} {
		if pair[0].kind != pair[1].kind || pair[0].support != pair[1].support {
			return false
		}
		if pair[0].offset == nil || pair[1].offset == nil {
			if pair[0].offset != nil || pair[1].offset != nil {
				return false
			}
		} else if !b.sameGroupExpression(pair[0].offset, pair[1].offset) {
			return false
		}
	}
	return true
}
func (b *expressionBinder) sameWindowSort(a, c boundWindowSpec) bool {
	if len(a.partition) != len(c.partition) || len(a.order) != len(c.order) {
		return false
	}
	for i, expr := range a.partition {
		if !b.sameGroupExpression(expr, c.partition[i]) {
			return false
		}
	}
	for i, key := range a.order {
		other := c.order[i]
		if key.desc != other.desc || key.nullsFirst != other.nullsFirst || !b.sameGroupExpression(key.expression, other.expression) {
			return false
		}
	}
	return true
}
func (b *expressionBinder) finishWindows(root LogicalNode, keys []BoundExpr, grouped bool, projection []BoundExpr, order, on []sortKey, stringCompanions ...[]BoundExpr) (LogicalNode, error) {
	if grouped {
		for _, definition := range b.windowRaw {
			spec := b.windowDefinitions[definition.Name.Text]
			for _, key := range spec.partition {
				if _, err := b.rewriteGroup(key, keys); err != nil {
					return nil, err
				}
			}
			for _, key := range spec.order {
				if _, err := b.rewriteGroup(key.expression, keys); err != nil {
					return nil, err
				}
			}
			for _, bound := range [2]boundWindowBound{spec.frame.start, spec.frame.end} {
				if bound.offset != nil {
					if _, err := b.rewriteGroup(bound.offset, keys); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if len(b.windowCalls) == 0 {
		return root, nil
	}
	preparedCalls := append([]windowCall(nil), b.windowCalls...)
	for i, call := range preparedCalls {
		spec := call.spec
		spec.partition = append([]BoundExpr(nil), spec.partition...)
		spec.order = append([]sortKey(nil), spec.order...)
		if grouped {
			if call.argument != nil {
				next, err := b.rewriteGroup(call.argument, keys)
				if err != nil {
					return nil, err
				}
				preparedCalls[i].argument = next
			}
			if call.ordinal != nil {
				next, err := b.rewriteGroup(call.ordinal, keys)
				if err != nil {
					return nil, err
				}
				preparedCalls[i].ordinal = next
			}
			if call.defaultValue != nil {
				next, err := b.rewriteGroup(call.defaultValue, keys)
				if err != nil {
					return nil, err
				}
				preparedCalls[i].defaultValue = next
			}
			for j, key := range spec.partition {
				next, err := b.rewriteGroup(key, keys)
				if err != nil {
					return nil, err
				}
				spec.partition[j] = next
			}
			for j, key := range spec.order {
				next, err := b.rewriteGroup(key.expression, keys)
				if err != nil {
					return nil, err
				}
				spec.order[j].expression = next
			}
			var err error
			if spec.frame.start.offset != nil {
				spec.frame.start.offset, err = b.rewriteGroup(spec.frame.start.offset, keys)
				if err != nil {
					return nil, err
				}
			}
			if spec.frame.end.offset != nil {
				spec.frame.end.offset, err = b.rewriteGroup(spec.frame.end.offset, keys)
				if err != nil {
					return nil, err
				}
			}
		}
		preparedCalls[i].spec = spec
	}
	aliases := b.windowValueAliases(preparedCalls, projection, order, on)
	var err error
	calls := make([]windowCall, 0, len(b.windowCalls))
	specs := make([]boundWindowSpec, 0, len(b.windowCalls))
	indices := make([]int, 0, len(b.windowCalls))
	schema := append([]ColumnDescriptor(nil), root.Schema()...)
	for i, call := range preparedCalls {
		if call.analysisOnly || aliases != nil && aliases[i] >= 0 {
			continue
		}
		spec := call.spec
		index := -1
		for j, other := range specs {
			if b.sameWindowSpec(spec, other) {
				index = j
				break
			}
		}
		if index < 0 {
			index = len(specs)
			specs = append(specs, spec)
		}
		shared := -1
		for j, previous := range calls {
			if indices[j] == index && (call.operation == windowNumber || call.operation == windowCount) && previous.operation == call.operation {
				shared = j
				break
			}
		}
		if shared >= 0 {
			call.expression.slot = calls[shared].expression.slot
			continue
		}
		call.expression.slot = len(schema)
		call.spec = boundWindowSpec{}
		calls = append(calls, call)
		indices = append(indices, index)
		schema = append(schema, ColumnDescriptor{Usage: call.expression.ResultUsage(), Nullable: call.expression.nullable, NullabilityKnown: true})
	}
	for i, owner := range aliases {
		if owner >= 0 {
			b.windowCalls[i].expression.slot = preparedCalls[owner].expression.slot
			b.windowCalls[i].analysisOnly = true
		}
	}
	originalWidth := len(root.Schema())
	root, err = b.lowerStringWindowSpecs(root, specs, calls, stringCompanions)
	if err != nil {
		return nil, err
	}
	if added := len(root.Schema()) - originalWidth; added != 0 {
		schema = append(append([]ColumnDescriptor(nil), root.Schema()...), schema[originalWidth:]...)
		for _, call := range b.windowCalls {
			call.expression.slot += added
		}
	}
	sortIndices := make([]int, len(specs))
	for i, spec := range specs {
		sortIndices[i] = i
		for j := range i {
			if b.sameWindowSort(spec, specs[j]) {
				sortIndices[i] = sortIndices[j]
				break
			}
		}
	}
	return &windowNode{querySchema: schema, child: root, specs: specs, calls: calls, specIndices: indices, sortIndices: sortIndices}, nil
}

func windowOccurrence(expr BoundExpr) *windowExpression {
	var found *windowExpression
	walkBoundExpression(expr, func(child BoundExpr) {
		if e, ok := child.(*windowExpression); ok && found == nil {
			found = e
		}
	})
	return found
}
