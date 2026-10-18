package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSum(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	// VALUES has no source namespace: the destination is not a SUM input.
	defer b.valuesAggregateNamespace()()
	var operand comparisonOperand
	var singleType [1]string
	var types []string
	if len(raw.Args) == 1 {
		types = singleType[:]
	} else if len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	// Children and FILTER are analyzed before signature resolution.
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = o
		}
		if types != nil {
			types[i] = b.diagnosticTypeName(o.descriptor())
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := b.resolveSumSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	provider, operation, err := b.sumProvider(arg.ResultUsage().Type)
	if err != nil {
		return nil, err
	}
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, "sum", arg.ResultUsage().Type.ID())
	if err != nil {
		return nil, err
	}
	return b.finishAggregate(raw, call, filter, b.info(provider.ResultUsage(), raw.Pos, ExprProperties{}))
}

func (b *expressionBinder) sumProvider(typ *sqltype.Descriptor) (functions.AggregateProvider, functions.AggregateOperation, error) {
	switch typ.ID() {
	case sqltype.Int2, sqltype.Int4:
		return functions.NewIntegralSum(typ, b.typ(sqltype.Int8).Usage()), functions.AggregateIntegralSum, nil
	case sqltype.Int8, sqltype.Numeric:
		return functions.NewNumericSum(typ, b.typ(sqltype.Numeric).Usage()), functions.AggregateNumericSum, nil
	case sqltype.Float4, sqltype.Float8:
		return functions.NewFloatSum(typ, typ.Usage()), functions.AggregateFloatSum, nil
	case sqltype.Money:
		bound, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, typ, typ, typ)
		if err != nil {
			return nil, functions.AggregateMoneySum, err
		}
		return moneySumProvider{typ.Usage(), bound}, functions.AggregateMoneySum, nil
	case sqltype.Interval:
		return intervalSumProvider{typ.Usage()}, functions.AggregateIntervalSum, nil
	}
	return nil, functions.AggregateCount, nil
}

// Resolve/coerce SUM's existing callable without constructing a provider or
// registering ordinary aggregation; excluded window execution shares this seam.
func (b *expressionBinder) resolveSumSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
	typ := operand.descriptor()
	if typ == nil {
		d := expressionError("42725", fmt.Sprintf("function %s(unknown) is not unique", objectNameText(name)), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, d
	}
	typ = typ.BaseType()
	switch typ.ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8, sqltype.Money, sqltype.Interval:
		return b.coerceConditional(operand, typ.Usage(), raw.Name)
	default:
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
}

func sumSignatureError(name parser.ObjectName, pos parser.Position, types []string, orderKeys int) error {
	d := functionSignatureErrorForTypes(name, pos, types).(*sqlstate.Error)
	if orderKeys > 1 {
		d.Hint = "No aggregate function matches the given name and argument types. Perhaps you misplaced ORDER BY; ORDER BY must appear after all regular arguments of the aggregate."
	}
	return d
}

// Inspect selected SUM/bitwise/JSON scalar composition without crossing a child query's frame.
// Only failed analysis (nil bound source) uses this check; assignment failures
// with an actual source retain the destination's ordinary diagnostic.
func containsValuesAggregateAnalysis(raw parser.Expr) bool {
	var children []parser.Expr
	switch e := raw.(type) {
	case parser.AggregateExpr:
		if e.Name == "sum" || e.Name == "bit_and" || e.Name == "bit_or" || e.Name == "bit_xor" || jsonArrayAggregateName(e.Name) || jsonObjectAggregateName(e.Name) || orderedSetName(e.Name) {
			return true
		}
		for _, key := range e.Order {
			if containsValuesAggregateAnalysis(key.Expr) {
				return true
			}
		}
		if containsValuesAggregateAnalysis(e.Filter) {
			return true
		}
		children = e.Args
	case parser.CastExpr:
		return containsValuesAggregateAnalysis(e.Child)
	case parser.CollateExpr:
		return containsValuesAggregateAnalysis(e.Child)
	case parser.RegclassExpr:
		return containsValuesAggregateAnalysis(e.Child)
	case parser.TimeZoneExpr:
		return containsValuesAggregateAnalysis(e.Value) || !e.Local && containsValuesAggregateAnalysis(e.Zone)
	case parser.SQLJSONExpr:
		children = e.Args
	case parser.JSONPredicateExpr:
		children = []parser.Expr{e.Child}
	case parser.FunctionExpr:
		children = e.Args
	case parser.CallExpr:
		children = e.Args
	case parser.InListExpr:
		if containsValuesAggregateAnalysis(e.Left) {
			return true
		}
		children = e.Items
	case parser.ArrayComparisonExpr:
		return containsValuesAggregateAnalysis(e.Left) || containsValuesAggregateAnalysis(e.Right)
	case parser.ArrayExpr:
		children = e.Elements
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		for _, child := range e.Children(&storage) {
			if containsValuesAggregateAnalysis(child) {
				return true
			}
		}
		return false
	case parser.FieldExpr:
		return containsValuesAggregateAnalysis(e.Base)
	case parser.RowExpr:
		children = e.Fields
	case parser.SubqueryExpr:
		return containsValuesAggregateAnalysis(e.Left)
	case parser.OperatorExpr:
		children = e.Args
	case parser.ConditionalExpr:
		children = e.Args
	case parser.LogicalExpr:
		return containsValuesAggregateAnalysis(e.Left) || containsValuesAggregateAnalysis(e.Right)
	case parser.PredicateExpr:
		return containsValuesAggregateAnalysis(e.Left) || containsValuesAggregateAnalysis(e.Right)
	case parser.CaseExpr:
		if containsValuesAggregateAnalysis(e.Test) || containsValuesAggregateAnalysis(e.Else) {
			return true
		}
		for _, arm := range e.Arms {
			if containsValuesAggregateAnalysis(arm.Condition) || containsValuesAggregateAnalysis(arm.Result) {
				return true
			}
		}
	}
	for _, child := range children {
		if containsValuesAggregateAnalysis(child) {
			return true
		}
	}
	return false
}
