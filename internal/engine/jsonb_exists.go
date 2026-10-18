package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonbExistenceName(name string) bool { return name == "?" || name == "?|" || name == "?&" }

func (b *expressionBinder) bindJSONBExistence(raw parser.OperatorExpr) (BoundExpr, error) {
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
	signature := func() error {
		return b.arrayMembershipError(left, right, raw)
	}
	// Unary spelling is parsed by the existing generic-operator grammar but
	// has no selected executable signature.
	if len(raw.Args) != 2 {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	if !visible {
		return nil, signature()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if name == "?|" && left == nil && right == nil {
		return nil, b.comparisonError(sqlError("42725", "operator is not unique"), left, right, raw)
	}
	targets := [2]sqltype.Usage{b.typ(sqltype.JSONB).Usage(), b.typ(sqltype.Text).Usage()}
	if name != "?" {
		array, ok := b.bindingCatalog().LookupArray(b.typ(sqltype.Text))
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing text array association")
		}
		targets[1] = array.Usage()
	}
	for i, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), targets[i], sqltype.Implicit); err != nil {
				return nil, signature()
			}
		}
	}
	args := make([]BoundExpr, 2)
	properties := ExprProperties{}
	for i, o := range operands {
		args[i], err = b.coerceConditional(o, targets[i], name)
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, args[i].Properties())
	}
	return functions.NewBinary[BoundExpr, ExprContext](functionInfo(b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties)), name, args, parser.OrdinaryFunctionSyntax)
}
