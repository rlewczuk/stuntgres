package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func orderedSetName(name string) bool {
	return name == "percentile_cont" || name == "percentile_disc" || name == "mode" || hypotheticalSetName(name)
}

func (b *expressionBinder) bindOrderedSet(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var localOperands [2]comparisonOperand
	var localTypes [2]string
	n := len(raw.Args)
	if raw.Within {
		n += len(raw.Order)
	}
	operands, types := localOperands[:], localTypes[:]
	if n > 2 {
		operands = make([]comparisonOperand, n)
		types = make([]string, n)
	} else {
		operands, types = operands[:n], types[:n]
	}
	for i := range n {
		var child parser.Expr
		if i < len(raw.Args) {
			child = raw.Args[i]
		} else {
			child = raw.Order[i-len(raw.Args)].Expr
		}
		operand, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		operands[i] = operand
		types[i], err = b.extremaArgumentTypeName(operand)
		if err != nil {
			return nil, err
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	directCount := 1
	operation := functions.AggregatePercentileCont
	if raw.Name == "mode" {
		directCount = 0
		operation = functions.AggregateMode
	} else if raw.Name == "percentile_disc" {
		operation = functions.AggregatePercentileDisc
	}
	undefined := func() (BoundExpr, error) { return nil, functionSignatureErrorForTypes(name, raw.Pos, types) }
	if !visible || n != directCount+1 {
		return undefined()
	}
	inputOperand := operands[n-1]
	inputType := inputOperand.descriptor()
	array := false
	var direct []BoundExpr
	fraction := b.typ(sqltype.Float8)
	if directCount == 1 {
		fractionType := fraction
		if typ := operands[0].descriptor(); typ != nil && typ.BaseType().IsArray() {
			array = true
			fractionType = b.typ(fraction.AssociatedArray())
		}
		if typ := operands[0].descriptor(); typ != nil {
			if _, err := sqltype.PlanCoercion(typ.Usage(), fractionType.Usage(), sqltype.Implicit); err != nil {
				return undefined()
			}
		}
		direct = make([]BoundExpr, 1)
		direct[0], err = b.coerceConditional(operands[0], fractionType.Usage(), raw.Name)
		if err != nil {
			return nil, err
		}
	}
	if operation == functions.AggregatePercentileCont {
		selected := fraction
		if inputType != nil && inputType.BaseType().ID() == sqltype.Interval {
			selected = b.typ(sqltype.Interval)
		}
		if inputType != nil {
			if _, err := sqltype.PlanCoercion(inputType.Usage(), selected.Usage(), sqltype.Implicit); err != nil {
				return undefined()
			}
		}
		inputType = selected
	} else if inputType == nil {
		inputType = b.typ(sqltype.Text)
	}
	if !raw.Within {
		return nil, expressionError("42809", "WITHIN GROUP is required for ordered-set aggregate "+raw.Name, raw.Pos)
	}
	if raw.Window != nil {
		return nil, expressionError("0A000", "OVER is not supported for ordered-set aggregate "+raw.Name, raw.Pos)
	}
	if len(raw.Args) != directCount {
		d := functionSignatureErrorForTypes(name, raw.Pos, types).(*sqlstate.Error)
		d.Hint = fmt.Sprintf("There is an ordered-set aggregate %s, but it requires %d direct argument, not %d.", raw.Name, directCount, len(raw.Args))
		return nil, d
	}
	if len(raw.Order) != 1 {
		return undefined()
	}
	arg, err := b.coerceConditional(inputOperand, inputType.Usage(), raw.Name)
	if err != nil {
		return nil, err
	}
	usage := arg.ResultUsage()
	usage.Typmod = -1
	if arg.CollationIndeterminate() {
		left, right, position := arg.ImplicitCollationConflict()
		d := expressionError("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
		d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
		return nil, d
	}
	comparison, err := aggregateOrdering(arg.ResultUsage(), raw.Order[0].Pos, false)
	if err != nil {
		return nil, err
	}
	if array {
		result, ok := b.definitionArray(usage.Type)
		if !ok || !result.Concrete() {
			return nil, sqlError("42704", "could not find array type for data type "+b.diagnosticTypeName(usage.Type))
		}
		usage.Type = result
	}
	provider := &orderedSetProvider{usage: usage, input: inputType, fraction: fraction, operation: operation, array: array, comparison: comparison}
	if operation == functions.AggregatePercentileCont && inputType.ID() == sqltype.Interval {
		provider.subtract, err = sqltype.BindArithmetic(sqltype.ArithmeticSubtract, inputType, inputType, inputType)
		if err != nil {
			return nil, err
		}
		provider.multiply, err = sqltype.BindArithmetic(sqltype.ArithmeticMultiply, inputType, fraction, inputType)
		if err != nil {
			return nil, err
		}
		provider.add, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, inputType, inputType, inputType)
		if err != nil {
			return nil, err
		}
	}
	key := raw.Order[0]
	call := aggregateCall{provider: provider, argument: arg, direct: direct, operation: operation, stageWidth: 2, order: []sortKey{{expression: arg, comparison: comparison, position: key.Pos.Character, desc: key.Desc, nullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc, slot: 0, cache: 0}}}
	var formal [2]sqltype.ID
	formalCount := 2
	if operation == functions.AggregateMode {
		formal[0], formalCount = sqltype.ID(2283), 1
	} else {
		formal[0] = sqltype.Float8
		if array {
			formal[0] = sqltype.ID(1022)
		}
		formal[1] = sqltype.ID(2283)
		if operation == functions.AggregatePercentileCont {
			formal[1] = inputType.ID()
		}
	}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, raw.Name, formal[:formalCount]...)
	if err != nil {
		return nil, err
	}
	info := b.info(usage, raw.Pos, ExprProperties{})
	info.CollationConflict, info.CollationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) rejectOrdinaryWithin(raw parser.AggregateExpr) (BoundExpr, error) {
	combined := raw
	combined.Args = make([]parser.Expr, 0, len(raw.Args)+len(raw.Order))
	combined.Args = append(combined.Args, raw.Args...)
	for _, key := range raw.Order {
		combined.Args = append(combined.Args, key.Expr)
	}
	combined.Within, combined.Order, combined.Window = false, nil, nil
	if _, err := b.bind(combined, expressionPolicy{context: projectionContext}); err != nil {
		return nil, err
	}
	return nil, expressionError("42809", raw.Name+" is not an ordered-set aggregate, so it cannot have WITHIN GROUP", raw.Pos)
}

func (b *expressionBinder) rewriteOrderedSetDirect(expr BoundExpr, keys []BoundExpr) (BoundExpr, error) {
	direct, err := b.rewriteGroup(expr, keys)
	if d, ok := err.(*sqlstate.Error); ok && d.Code == "42803" {
		copy := *d
		copy.Detail = "Direct arguments of an ordered-set aggregate must use only grouped columns."
		return nil, &copy
	}
	return direct, err
}
