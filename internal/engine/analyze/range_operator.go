package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func rangeSharedOperator(raw parser.OperatorExpr) bool {
	if len(raw.Name.Parts) == 0 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return false
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	_, ok := sqltype.RangeOperatorKind(name)
	return ok && (len(raw.Args) == 2 || !arithmeticName(name))
}

// Analyze shared symbolic operands once, then hand the existing binders their
// analyzed occurrences. UNKNOWN inference remains occurrence-owned.
func (b *Binder) bindRangeShared(raw parser.OperatorExpr) (expr.Expr, error) {
	var operands [2]ComparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	left, right := operands[0].descriptor(), operands[1].descriptor()
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if len(raw.Args) == 1 {
		if _, err := b.builtinVisible(raw.Name, OperatorLookup); err != nil {
			return nil, err
		}
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), left, nil, raw)
	}
	if _, point := sqltype.PointKind(name); point && (pointOperand(left) || pointOperand(right)) {
		return b.finishPointOperator(raw, operands)
	}
	if _, polygon := sqltype.PolygonKind(name); polygon && (polygonOperand(left) || polygonOperand(right)) {
		return b.finishPolygonOperator(raw, operands)
	}
	if op, circle := sqltype.CircleKind(name); circle && op != sqltype.CircleRadius &&
		(circleOperand(left) || circleOperand(right)) {
		return b.finishCircleOperator(raw, operands)
	}
	if _, subnet := sqltype.NetworkSubnetKind(name); subnet && (networkSubnetOperand(left) || networkSubnetOperand(right)) {
		return b.finishNetworkSubnet(raw, operands)
	}
	if !sqltype.IsRangeInspectType(left) && !sqltype.IsRangeInspectType(right) && (left != nil || right != nil) {
		if ArrayMembershipName(name) {
			return b.finishArrayMembership(raw, operands)
		}
		if arithmeticName(name) {
			return b.finishArithmetic(raw, operands[0], operands[1])
		}
		if name == "<<" || name == ">>" {
			visible, err := b.builtinVisible(raw.Name, OperatorLookup)
			if err != nil {
				return nil, err
			}
			return b.finishBitwise(raw, operands[0], operands[1], visible)
		}
	}
	visible, err := b.builtinVisible(raw.Name, OperatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, functions.ArrayOperatorError(b, left, right, raw)
	}
	if left == nil && right == nil {
		d := expr.ErrorAt("42725", "operator is not unique: unknown "+name+" unknown", raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
		return nil, d
	}
	// binary_oper_exact retries a known domain's physical family for UNKNOWN.
	lt, rt := left, right
	if lt == nil {
		lt = rt.BaseType()
	} else {
		lt = lt.BaseType()
	}
	if rt == nil {
		rt = lt
	} else {
		rt = rt.BaseType()
	}
	op, _ := sqltype.RangeOperatorKind(name)
	result := sqltype.RangeOperatorResult(op, lt, rt)
	if result == nil {
		return nil, functions.ArrayOperatorError(b, left, right, raw)
	}
	var args [2]expr.Expr
	for i, target := range [2]*sqltype.Descriptor{lt, rt} {
		args[i], err = b.CoerceConditional(operands[i], target.Usage(), name)
		if err != nil {
			return nil, err
		}
	}
	properties := expr.MergeProperties(args[0].Properties(), args[1].Properties())
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info := b.Info(result.Usage(), parser.ExpressionStart(raw), properties)
	node, err := functions.NewRangeOperator(info, op, args, namespace)
	if err != nil {
		return nil, err
	}
	var formal [2]sqltype.ID
	for i, typ := range [2]*sqltype.Descriptor{lt, rt} {
		formal[i] = rangeRoutineFormalID(typ)
		if formal[i] == 0 {
			formal[i] = 2283 // ANYELEMENT, only after genuine range-plan selection.
		}
	}
	info, err = b.builtinOperatorRoutineInfo(info, name, formal[0], formal[1])
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
