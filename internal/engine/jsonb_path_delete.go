package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindJSONBPathDelete(raw parser.OperatorExpr) (BoundExpr, error) {
	var operands [2]comparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	left, right := operands[0].descriptor(), operands[1].descriptor()
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) != 2 {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	if !visible {
		return nil, b.arrayMembershipError(left, right, raw)
	}
	array, ok := b.bindingCatalog().LookupArray(b.typ(sqltype.Text))
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing text array association")
	}
	targets := [2]sqltype.Usage{b.typ(sqltype.JSONB).Usage(), array.Usage()}
	for i, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), targets[i], sqltype.Implicit); err != nil {
				return nil, b.arrayMembershipError(left, right, raw)
			}
		}
	}
	args := make([]BoundExpr, 2)
	properties := ExprProperties{}
	for i, o := range operands {
		args[i], err = b.coerceConditional(o, targets[i], "#-")
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, args[i].Properties())
	}
	return functions.NewBinary[BoundExpr, *EvalContext](functionInfo(b.info(targets[0], raw.Pos, properties)), "#-", args, parser.OrdinaryFunctionSyntax)
}
