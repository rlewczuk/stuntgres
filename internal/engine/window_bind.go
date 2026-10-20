package engine

import (
	"fmt"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A same-query call owns only its own argument occurrences. Evidence is checked
// after that call resolves, so an invalid signature is never hidden by nesting.
type argumentWindowOwner struct {
	Active, Window, Found bool
	First                 parser.Position
}

func (owner argumentWindowOwner) nestingError() error {
	if !owner.Found {
		return nil
	}
	if owner.Window {
		return expr.ErrorAt("42P20", "window function calls cannot be nested", owner.First)
	}
	return expr.ErrorAt("42803", "aggregate function calls cannot contain window function calls", owner.First)
}

func (b *expressionBinder) windowAllowed(pos parser.Position) error {
	if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
		return expr.ErrorAt("42P20", "window functions are not allowed in policy expressions", pos)
	}
	if b.Consumer.PartitionConsumer != "" {
		return b.Consumer.partitionExpressionError("42P20", "window functions are not allowed in "+b.Consumer.partitionContextPlural(), pos)
	}
	clause := b.Clause.AggregateClause
	if b.Consumer.CheckConsumer {
		clause = "check constraints"
	} else if b.Consumer.IndexPredicateConsumer {
		clause = "index predicates"
	} else if b.Consumer.IndexConsumer {
		clause = "index expressions"
	} else if b.Consumer.DefaultConsumer {
		clause = "DEFAULT expressions"
	}
	if clause == "window definitions" || clause == "window RANGE" || clause == "window GROUPS" || clause == "window ROWS" {
		return expr.ErrorAt("42P20", "window functions are not allowed in window definitions", pos)
	}
	if clause != "SELECT" && clause != "ORDER BY" && clause != "DISTINCT ON" {
		return expr.ErrorAt("42P20", "window functions are not allowed in "+clause, pos)
	}
	return nil
}
func (b *expressionBinder) bindWindow(raw parser.AggregateExpr, name parser.ObjectName) (boundResult expr.Expr, bindError error) {
	parentOwner := b.Grouping.ArgumentOwner
	parentAggregates, parentWindows := b.Grouping.Aggregates, b.Grouping.WindowCalls
	defer func() {
		if bindError != nil {
			b.Grouping.Aggregates, b.Grouping.WindowCalls = parentAggregates, parentWindows
		}
	}()
	b.Grouping.ArgumentOwner = argumentWindowOwner{Active: true, Window: true}
	defer func() { b.Grouping.ArgumentOwner = parentOwner }()
	var resultUsage sqltype.Usage
	var argument, ordinal, defaultValue expr.Expr
	// Window functions resolve their own signature; aggregates resolve as
	// they would without OVER, minus provider and group state.
	function, windowFunction := functions.LookupWindowFunction(raw.Name)
	if windowFunction {
		signature, err := function.Signature(b, raw, name)
		if err != nil {
			return nil, err
		}
		argument, ordinal, defaultValue, resultUsage = signature.Argument, signature.Ordinal, signature.Default, signature.Usage
	} else {
		var err error
		resultUsage, err = b.ResolveWindowAggregateSignature(raw, name, &argument)
		if err != nil {
			return nil, err
		}
	}
	if raw.Distinct {
		return nil, expr.ErrorAt("0A000", "DISTINCT is not implemented for window functions", raw.Pos)
	}
	if len(raw.Order) > 0 {
		return nil, expr.ErrorAt("0A000", "aggregate ORDER BY is not implemented for window functions", raw.Pos)
	}
	if raw.Filter != nil && windowFunction {
		return nil, expr.ErrorAt("0A000", "FILTER is not implemented for non-aggregate window functions", raw.Pos)
	}
	if err := b.windowAllowed(raw.Pos); err != nil {
		return nil, err
	}
	if err := b.Grouping.ArgumentOwner.nestingError(); err != nil {
		return nil, err
	}
	if raw.Window.Name != nil && raw.Filter == nil && (windowFunction || raw.Name == "sum" || (raw.Name == "count" && raw.Star)) {
		found := false
		for _, definition := range b.Grouping.windowRaw {
			if definition.Name.Text == raw.Window.Name.Text {
				found = true
				break
			}
		}
		if !found {
			return nil, expr.ErrorAt("42704", fmt.Sprintf(`window "%s" does not exist`, raw.Window.Name.Text), raw.Window.Pos)
		}
	}
	if parentOwner.Active {
		if !parentOwner.Found {
			parentOwner.First, parentOwner.Found = raw.Pos, true
		}
		// Analysis-only occurrence: the completed parent must either reject its
		// signature or report nesting. It cannot enter an executable window plan.
		return &plan.WindowExpr{Info: b.Info(resultUsage, raw.Pos, expr.Properties{StageDependent: true}), Slot: -1, Call: -1, Frame: b.FrameID()}, nil
	}
	if raw.Filter != nil {
		return nil, expr.ErrorAt("0A000", "window aggregate FILTER is unsupported", raw.FilterPos)
	}
	if !windowFunction && raw.Name != "sum" && (raw.Name != "count" || !raw.Star) {
		return nil, expr.ErrorAt("0A000", "window function provider is unsupported", raw.Pos)
	}
	call := plan.WindowCall{Raw: raw.Window, Argument: argument, Ordinal: ordinal, DefaultValue: defaultValue}
	switch raw.Name {
	case "row_number":
		call.Operation = plan.WindowNumber
	case "rank":
		call.Operation = plan.WindowRank
	case "dense_rank":
		call.Operation = plan.WindowDenseRank
	case "percent_rank":
		call.Operation = plan.WindowPercentRank
	case "cume_dist":
		call.Operation = plan.WindowCumeDist
	case "ntile":
		call.Operation = plan.WindowNtile
	case "lead":
		call.Operation = plan.WindowLead
	case "lag":
		call.Operation = plan.WindowLag
	case "first_value":
		call.Operation = plan.WindowFirst
	case "last_value":
		call.Operation = plan.WindowLast
	case "nth_value":
		call.Operation = plan.WindowNth
	case "sum":
		call.Operation = plan.WindowSum
		// Ordinary SUM has additional providers; windows remain numeric-only.
		switch argument.ResultUsage().Type.ID() {
		case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8:
		default:
			return nil, expr.ErrorAt("0A000", "window function provider is unsupported", raw.Pos)
		}
		var err error
		call.Provider, _, err = functions.SumProvider(b, argument.ResultUsage().Type)
		if err != nil {
			return nil, err
		}
	}
	var formal [3]sqltype.ID
	formalCount := 0
	if raw.Name == "sum" {
		formal[0], formalCount = argument.ResultUsage().Type.ID(), 1
	} else if raw.Name == "ntile" {
		formal[0], formalCount = sqltype.Int4, 1
	} else if functions.WindowOffsetName(raw.Name) {
		formal[0], formalCount = 2283, 1 // Released ANYELEMENT for arity one/two.
		if len(raw.Args) >= 2 {
			formal[1], formalCount = sqltype.Int4, 2
		}
		if len(raw.Args) == 3 {
			formal[0], formal[2], formalCount = 5077, 5077, 3 // ANYCOMPATIBLE value/default.
		}
	} else if functions.WindowValueName(raw.Name) {
		formal[0], formalCount = 2283, 1 // Released ANYELEMENT, not the physical selected value type.
		if raw.Name == "nth_value" {
			formal[1], formalCount = sqltype.Int4, 2
		}
	}
	var err error
	call.Routine, err = b.builtinRoutineReference(functions.WindowRoutineProvider, raw.Name, formal[:formalCount]...)
	if err != nil {
		return nil, err
	}
	info := b.Info(resultUsage, raw.Pos, expr.Properties{StageDependent: true})
	if raw.Name == "ntile" {
		info.Property = expr.MergeProperties(info.Property, argument.Properties())
	}
	if functions.WindowValueName(raw.Name) || functions.WindowOffsetName(raw.Name) {
		info.Property = expr.MergeProperties(info.Property, argument.Properties())
		if ordinal != nil {
			info.Property = expr.MergeProperties(info.Property, ordinal.Properties())
		}
		var err error
		if defaultValue != nil {
			info.Property = expr.MergeProperties(info.Property, defaultValue.Properties())
			info, err = b.CollatedInfo(info, argument, defaultValue)
		} else {
			info, err = b.CollatedInfo(info, argument)
		}
		if err != nil {
			return nil, err
		}
	}
	expr := &plan.WindowExpr{Info: info, Slot: -1, Frame: b.FrameID(), Call: len(b.Grouping.WindowCalls)}
	call.Expression = expr
	expr.Nullable = call.Operation == plan.WindowSum || call.Operation == plan.WindowNtile || functions.WindowValueName(raw.Name) || functions.WindowOffsetName(raw.Name)
	b.Grouping.WindowCalls = append(b.Grouping.WindowCalls, call)
	return expr, nil
}

// The excluded-provider path performs the same signature/coercion analysis as
// ordinary aggregation, but never builds a provider or registers an aggregate.
// In particular, raw call-local ORDER expressions are not transformed here.
func (b *expressionBinder) ResolveWindowAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, argument *expr.Expr) (sqltype.Usage, error) {
	if raw.Name == "int_array_aggregate" {
		call := parser.FunctionExpr{Name: name, Args: raw.Args, Star: raw.Star, Variadic: raw.Variadic, Pos: raw.Pos}
		arg, _, _, _, err := b.resolveIntaggAggregate(raw, call)
		if err != nil {
			return sqltype.Usage{}, err
		}
		*argument = arg
		array, ok := b.definitionArray(b.Type(sqltype.Int4))
		if !ok {
			return sqltype.Usage{}, fmt.Errorf("stuntgres: missing integer array descriptor")
		}
		return array.Usage(), nil
	}
	arg, usage, err := functions.ResolveWindowAggregate(b, raw, name)
	*argument = arg
	return usage, err
}

// Resolve keys only after target, predicate, grouping and outer order analysis.
func (b *expressionBinder) resolveWindows() error {
	if err := b.resolveWindowDefinitions(); err != nil {
		return err
	}
	for i := range b.Grouping.WindowCalls {
		spec, err := b.bindWindowSpec(*b.Grouping.WindowCalls[i].Raw, b.Grouping.windowDefinitions, true)
		if err != nil {
			return err
		}
		b.Grouping.WindowCalls[i].Spec = spec
		b.Grouping.WindowCalls[i].Raw = nil
	}
	return nil
}

func (b *expressionBinder) SameWindowCall(a, c plan.WindowCall) bool {
	if a.Operation != c.Operation {
		return false
	}
	// Arguments can own child queries hidden by the window leaf. This is
	// semantic target equality, not permission to merge volatile executions.
	if (a.Operation == plan.WindowSum || a.Operation.EvaluatesArgument()) && (!sqltype.SameRecordUsage(a.Expression.ResultUsage(), c.Expression.ResultUsage()) || !b.SameQueryTarget(a.Argument, c.Argument) || !b.SameQueryTarget(a.Ordinal, c.Ordinal) || !b.SameQueryTarget(a.DefaultValue, c.DefaultValue)) {
		return false
	}
	if a.Raw != nil || c.Raw != nil {
		return sameRawWindow(reflect.ValueOf(a.Raw), reflect.ValueOf(c.Raw))
	}
	return b.SameWindowSpec(a.Spec, c.Spec)
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
	if b.Grouping.windowDefinitions != nil {
		return nil
	}
	if len(b.Grouping.windowRaw) == 0 {
		return nil
	}
	b.Grouping.windowDefinitions = make(map[string]plan.WindowSpec, len(b.Grouping.windowRaw))
	for definitionIndex, definition := range b.Grouping.windowRaw {
		if _, ok := b.Grouping.windowDefinitions[definition.Name.Text]; ok {
			return expr.ErrorAt("42P20", fmt.Sprintf(`window "%s" is already defined`, definition.Name.Text), definition.Spec.Pos)
		}
		spec, err := b.bindWindowSpec(definition.Spec, b.Grouping.windowDefinitions, false)
		if err != nil {
			return err
		}
		spec.Definition = definitionIndex + 1
		if b.Params.NoParameters || b.Consumer.materializedDefinition {
			ordinal := 0
			path := viewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewWindowLiteral, Target: definitionIndex}
			for i, key := range definition.Spec.Partition {
				if err := b.CaptureViewLiteralTree(key, spec.Partition[i], path, &ordinal); err != nil {
					return err
				}
			}
			for i, key := range definition.Spec.Order {
				if err := b.CaptureViewLiteralTree(key.Expr, spec.Order[spec.OrderMap[i]].Expression, path, &ordinal); err != nil {
					return err
				}
			}
			if definition.Spec.Frame != nil {
				if err := b.CaptureViewLiteralTree(definition.Spec.Frame.Start.Offset, spec.Frame.Start.Offset, path, &ordinal); err != nil {
					return err
				}
				if err := b.CaptureViewLiteralTree(definition.Spec.Frame.End.Offset, spec.Frame.End.Offset, path, &ordinal); err != nil {
					return err
				}
			}
		}
		b.Grouping.windowDefinitions[definition.Name.Text] = spec
	}
	return nil
}
func (b *expressionBinder) bindWindowSpec(raw parser.WindowSpec, names map[string]plan.WindowSpec, overClause bool) (plan.WindowSpec, error) {
	var parent plan.WindowSpec
	if raw.Name != nil {
		var ok bool
		parent, ok = names[raw.Name.Text]
		if !ok {
			return plan.WindowSpec{}, expr.ErrorAt("42704", fmt.Sprintf(`window "%s" does not exist`, raw.Name.Text), raw.Pos)
		}
		if raw.Bare {
			return parent, nil
		}
	}
	old := b.Clause.AggregateClause
	b.Clause.AggregateClause = "window definitions"
	defer func() { b.Clause.AggregateClause = old }()
	spec := plan.WindowSpec{Order: make([]plan.SortKey, 0, len(raw.Order)), OrderMap: make([]int, len(raw.Order)), Partition: make([]expr.Expr, len(raw.Partition)), Grouping: make([]plan.Grouping, len(raw.Partition)), Explicit: raw.Frame != nil}
	spec.Frame.Start.Kind, spec.Frame.End.Kind = parser.FrameUnboundedPreceding, parser.FrameCurrentRow
	for i, key := range raw.Order {
		expr, err := b.Bind(key.Expr, expressionPolicy{Context: ProjectionContext})
		if err != nil {
			return spec, err
		}
		expr, err = b.FinalizeProjection(expr)
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
		for j, previous := range spec.Order {
			if b.SameGroupExpression(previous.Expression, expr) {
				index = j
				break
			}
		}
		if index < 0 {
			usage := expr.ResultUsage()
			base := usage.Type.BaseType()
			signature, err := sqltype.ResolveEquality(base, base)
			if err != nil {
				return spec, err
			}
			if signature.Left != base.ID() {
				usage.Type = b.Type(signature.Left)
			}
			routine, _, err := b.groupingRoutineReference(usage)
			if err != nil {
				return spec, err
			}
			index = len(spec.Order)
			spec.Order = append(spec.Order, plan.SortKey{Expression: expr, Comparison: comparison, EqualityRoutine: routine, Position: key.Pos.Character, Desc: key.Desc, NullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc})
		}
		spec.OrderMap[i] = index
	}
	for i, key := range raw.Partition {
		expr, err := b.Bind(key, expressionPolicy{Context: ProjectionContext})
		if err != nil {
			return spec, err
		}
		expr, err = b.FinalizeProjection(expr)
		if err != nil {
			return spec, err
		}
		grouping, err := b.bindDistinctGrouping(expr)
		if err != nil {
			return spec, err
		}
		spec.Partition[i], spec.Grouping[i] = expr, grouping
	}
	if raw.Name != nil {
		if len(raw.Partition) > 0 {
			return spec, expr.ErrorAt("42P20", fmt.Sprintf(`cannot override PARTITION BY clause of window "%s"`, raw.Name.Text), raw.Pos)
		}
		if len(parent.Order) > 0 && len(raw.Order) > 0 {
			return spec, expr.ErrorAt("42P20", fmt.Sprintf(`cannot override ORDER BY clause of window "%s"`, raw.Name.Text), raw.Pos)
		}
		if parent.Explicit {
			d := expr.ErrorAt("42P20", fmt.Sprintf(`cannot copy window "%s" because it has a frame clause`, raw.Name.Text), raw.Pos).(*sqlstate.Error)
			if overClause && len(raw.Partition) == 0 && len(raw.Order) == 0 && raw.Frame == nil {
				d.Hint = "Omit the parentheses in this OVER clause."
			}
			return spec, d
		}
		spec.Partition, spec.Grouping = parent.Partition, parent.Grouping
		if len(spec.Order) == 0 {
			spec.Order = parent.Order
		}
	}
	if raw.Frame != nil {
		spec.Frame.Unit = raw.Frame.Unit
		spec.Frame.Exclusion = raw.Frame.Exclusion
		if raw.Frame.Unit == parser.FrameGroups && len(spec.Order) == 0 {
			return spec, expr.ErrorAt("42P20", "GROUPS mode requires an ORDER BY clause", raw.Pos)
		}
		if raw.Frame.Unit == parser.FrameRange && (raw.Frame.Start.Offset != nil || raw.Frame.End.Offset != nil) && len(spec.Order) != 1 {
			return spec, expr.ErrorAt("42P20", "RANGE with offset PRECEDING/FOLLOWING requires exactly one ORDER BY column", raw.Pos)
		}
		var err error
		spec.Frame.Start, err = b.bindWindowBound(raw.Frame.Start, spec.Order, raw.Frame.Unit)
		if err != nil {
			return spec, err
		}
		spec.Frame.End, err = b.bindWindowBound(raw.Frame.End, spec.Order, raw.Frame.Unit)
		if err != nil {
			return spec, err
		}
	}
	return spec, nil
}
func (b *expressionBinder) SameWindowSpec(a, c plan.WindowSpec) bool {
	if a.Definition != c.Definition || a.Frame.Unit != c.Frame.Unit || a.Frame.Exclusion != c.Frame.Exclusion || !b.SameWindowSort(a, c) || a.Explicit != c.Explicit {
		return false
	}
	for _, pair := range [2][2]plan.WindowBound{{a.Frame.Start, c.Frame.Start}, {a.Frame.End, c.Frame.End}} {
		if pair[0].Kind != pair[1].Kind || pair[0].Support != pair[1].Support {
			return false
		}
		if pair[0].Offset == nil || pair[1].Offset == nil {
			if pair[0].Offset != nil || pair[1].Offset != nil {
				return false
			}
		} else if !b.SameGroupExpression(pair[0].Offset, pair[1].Offset) {
			return false
		}
	}
	return true
}
func (b *expressionBinder) SameWindowSort(a, c plan.WindowSpec) bool {
	if len(a.Partition) != len(c.Partition) || len(a.Order) != len(c.Order) {
		return false
	}
	for i, expr := range a.Partition {
		if !b.SameGroupExpression(expr, c.Partition[i]) {
			return false
		}
	}
	for i, key := range a.Order {
		other := c.Order[i]
		if key.Desc != other.Desc || key.NullsFirst != other.NullsFirst || !b.SameGroupExpression(key.Expression, other.Expression) {
			return false
		}
	}
	return true
}
func (b *expressionBinder) finishWindows(root plan.Node, keys []expr.Expr, grouped bool, projection []expr.Expr, order, on []plan.SortKey, stringCompanions ...[]expr.Expr) (plan.Node, error) {
	if grouped {
		for _, definition := range b.Grouping.windowRaw {
			spec := b.Grouping.windowDefinitions[definition.Name.Text]
			for _, key := range spec.Partition {
				if _, err := b.RewriteGroup(key, keys); err != nil {
					return nil, err
				}
			}
			for _, key := range spec.Order {
				if _, err := b.RewriteGroup(key.Expression, keys); err != nil {
					return nil, err
				}
			}
			for _, bound := range [2]plan.WindowBound{spec.Frame.Start, spec.Frame.End} {
				if bound.Offset != nil {
					if _, err := b.RewriteGroup(bound.Offset, keys); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if len(b.Grouping.WindowCalls) == 0 {
		return root, nil
	}
	preparedCalls := append([]plan.WindowCall(nil), b.Grouping.WindowCalls...)
	for i, call := range preparedCalls {
		spec := call.Spec
		spec.Partition = append([]expr.Expr(nil), spec.Partition...)
		spec.Order = append([]plan.SortKey(nil), spec.Order...)
		if grouped {
			if call.Argument != nil {
				next, err := b.RewriteGroup(call.Argument, keys)
				if err != nil {
					return nil, err
				}
				preparedCalls[i].Argument = next
			}
			if call.Ordinal != nil {
				next, err := b.RewriteGroup(call.Ordinal, keys)
				if err != nil {
					return nil, err
				}
				preparedCalls[i].Ordinal = next
			}
			if call.DefaultValue != nil {
				next, err := b.RewriteGroup(call.DefaultValue, keys)
				if err != nil {
					return nil, err
				}
				preparedCalls[i].DefaultValue = next
			}
			for j, key := range spec.Partition {
				next, err := b.RewriteGroup(key, keys)
				if err != nil {
					return nil, err
				}
				spec.Partition[j] = next
			}
			for j, key := range spec.Order {
				next, err := b.RewriteGroup(key.Expression, keys)
				if err != nil {
					return nil, err
				}
				spec.Order[j].Expression = next
			}
			var err error
			if spec.Frame.Start.Offset != nil {
				spec.Frame.Start.Offset, err = b.RewriteGroup(spec.Frame.Start.Offset, keys)
				if err != nil {
					return nil, err
				}
			}
			if spec.Frame.End.Offset != nil {
				spec.Frame.End.Offset, err = b.RewriteGroup(spec.Frame.End.Offset, keys)
				if err != nil {
					return nil, err
				}
			}
		}
		preparedCalls[i].Spec = spec
	}
	aliases := b.windowValueAliases(preparedCalls, projection, order, on)
	var err error
	calls := make([]plan.WindowCall, 0, len(b.Grouping.WindowCalls))
	specs := make([]plan.WindowSpec, 0, len(b.Grouping.WindowCalls))
	indices := make([]int, 0, len(b.Grouping.WindowCalls))
	schema := append([]catalog.Column(nil), root.Schema()...)
	for i, call := range preparedCalls {
		if call.AnalysisOnly || aliases != nil && aliases[i] >= 0 {
			continue
		}
		spec := call.Spec
		index := -1
		for j, other := range specs {
			if b.SameWindowSpec(spec, other) {
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
			if indices[j] == index && (call.Operation == plan.WindowNumber || call.Operation == plan.WindowCount) && previous.Operation == call.Operation {
				shared = j
				break
			}
		}
		if shared >= 0 {
			call.Expression.Slot = calls[shared].Expression.Slot
			continue
		}
		call.Expression.Slot = len(schema)
		call.Spec = plan.WindowSpec{}
		calls = append(calls, call)
		indices = append(indices, index)
		schema = append(schema, catalog.Column{Usage: call.Expression.ResultUsage(), Nullable: call.Expression.Nullable, NullabilityKnown: true})
	}
	for i, owner := range aliases {
		if owner >= 0 {
			b.Grouping.WindowCalls[i].Expression.Slot = preparedCalls[owner].Expression.Slot
			b.Grouping.WindowCalls[i].AnalysisOnly = true
		}
	}
	originalWidth := len(root.Schema())
	root, err = b.LowerStringWindowSpecs(root, specs, calls, stringCompanions)
	if err != nil {
		return nil, err
	}
	if added := len(root.Schema()) - originalWidth; added != 0 {
		schema = append(append([]catalog.Column(nil), root.Schema()...), schema[originalWidth:]...)
		for _, call := range b.Grouping.WindowCalls {
			call.Expression.Slot += added
		}
	}
	sortIndices := make([]int, len(specs))
	for i, spec := range specs {
		sortIndices[i] = i
		for j := range i {
			if b.SameWindowSort(spec, specs[j]) {
				sortIndices[i] = sortIndices[j]
				break
			}
		}
	}
	return &plan.WindowNode{QuerySchema: schema, Child: root, Specs: specs, Calls: calls, SpecIndices: indices, SortIndices: sortIndices}, nil
}

func WindowOccurrence(bound expr.Expr) *plan.WindowExpr {
	var found *plan.WindowExpr
	plan.WalkExpression(bound, func(child expr.Expr) {
		if e, ok := child.(*plan.WindowExpr); ok && found == nil {
			found = e
		}
	})
	return found
}
