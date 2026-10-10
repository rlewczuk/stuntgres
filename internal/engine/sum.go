package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSum(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	if b.aggregateClause == "INSERT VALUES" && b.queryNamespace == nil {
		// VALUES has no source namespace: the destination is not a SUM input.
		b.queryNamespace = &queryNamespace{}
		hidden := b.queryHidden
		if target := b.insertTarget; target != nil {
			// Retain the target solely as an inaccessible diagnostic candidate.
			// It is never added to the source namespace or read manifest.
			columns := make([]queryColumn, len(target.columns))
			for i, descriptor := range target.columns {
				columns[i].descriptor = descriptor
			}
			b.queryHidden = append(b.queryHidden, queryQualifier{name: target.name, columns: columns, lateralForbidden: true})
		}
		defer func() {
			b.queryNamespace, b.queryHidden = nil, hidden
		}()
	}
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
	var provider aggregateProvider
	var operation aggregateOperation
	switch typ.ID() {
	case sqltype.Int2, sqltype.Int4:
		provider, operation = integralSumProvider{typ, b.typ(sqltype.Int8).Usage()}, aggregateIntegralSum
	case sqltype.Int8, sqltype.Numeric:
		provider, operation = numericSumProvider{typ, b.typ(sqltype.Numeric).Usage()}, aggregateNumericSum
	case sqltype.Float4, sqltype.Float8:
		provider, operation = floatSumProvider{typ, typ.Usage()}, aggregateFloatSum
	case sqltype.Interval:
		// Retain the established clause diagnostic, without an interval executor.
		if b.aggregateClause == "RETURNING" {
			return nil, expressionError("42803", "aggregate functions are not allowed in RETURNING", raw.Pos)
		}
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	default:
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
	arg, err := b.coerceConditional(operand, typ.Usage(), raw.Name)
	if err != nil {
		return nil, err
	}
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	return b.finishAggregate(raw, call, filter, b.info(provider.ResultUsage(), raw.Pos, ExprProperties{}))
}

func sumSignatureError(name parser.ObjectName, pos parser.Position, types []string, orderKeys int) error {
	d := functionSignatureErrorForTypes(name, pos, types).(*sqlstate.Error)
	if orderKeys > 1 {
		d.Hint = "No aggregate function matches the given name and argument types. Perhaps you misplaced ORDER BY; ORDER BY must appear after all regular arguments of the aggregate."
	}
	return d
}

// Inspect admitted scalar composition without crossing a child query's frame.
// Only failed analysis (nil bound source) uses this check; assignment failures
// with an actual source retain the destination's ordinary diagnostic.
func containsSumAnalysis(raw parser.Expr) bool {
	var children []parser.Expr
	switch e := raw.(type) {
	case parser.AggregateExpr:
		if e.Name == "sum" {
			return true
		}
		for _, key := range e.Order {
			if containsSumAnalysis(key.Expr) {
				return true
			}
		}
		if containsSumAnalysis(e.Filter) {
			return true
		}
		children = e.Args
	case parser.CastExpr:
		return containsSumAnalysis(e.Child)
	case parser.CollateExpr:
		return containsSumAnalysis(e.Child)
	case parser.RegclassExpr:
		return containsSumAnalysis(e.Child)
	case parser.TimeZoneExpr:
		return containsSumAnalysis(e.Value) || !e.Local && containsSumAnalysis(e.Zone)
	case parser.FunctionExpr:
		children = e.Args
	case parser.CallExpr:
		children = e.Args
	case parser.ArrayExpr:
		children = e.Elements
	case parser.RowExpr:
		children = e.Fields
	case parser.SubqueryExpr:
		return containsSumAnalysis(e.Left)
	case parser.OperatorExpr:
		children = e.Args
	case parser.ConditionalExpr:
		children = e.Args
	case parser.LogicalExpr:
		return containsSumAnalysis(e.Left) || containsSumAnalysis(e.Right)
	case parser.PredicateExpr:
		return containsSumAnalysis(e.Left) || containsSumAnalysis(e.Right)
	case parser.CaseExpr:
		if containsSumAnalysis(e.Test) || containsSumAnalysis(e.Else) {
			return true
		}
		for _, arm := range e.Arms {
			if containsSumAnalysis(arm.Condition) || containsSumAnalysis(arm.Result) {
				return true
			}
		}
	}
	for _, child := range children {
		if containsSumAnalysis(child) {
			return true
		}
	}
	return false
}
