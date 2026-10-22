package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func networkSubnetOperand(t *sqltype.Descriptor) bool {
	return t != nil && sqltype.IsNetwork(t.BaseType().ID())
}
func (b *Binder) bindNetworkSubnet(raw parser.OperatorExpr) (expr.Expr, error) {
	var operands [2]ComparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	return b.finishNetworkSubnet(raw, operands)
}
func (b *Binder) finishNetworkSubnet(raw parser.OperatorExpr, operands [2]ComparisonOperand) (expr.Expr, error) {
	left, right := operands[0].descriptor(), operands[1].descriptor()
	visible, err := b.builtinVisible(raw.Name, OperatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, functions.ArrayOperatorError(b, left, right, raw)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	_, err = sqltype.ResolveOperator([]sqltype.OperatorInputs{{Left: sqltype.Inet, Right: sqltype.Inet}}, left, right, false)
	if err != nil {
		return nil, functions.OperatorError(err, left, right, raw)
	}
	var args [2]expr.Expr
	for i := range args {
		args[i], err = b.CoerceConditional(operands[i], b.Type(sqltype.Inet).Usage(), name)
		if err != nil {
			return nil, err
		}
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info := b.Info(b.Type(sqltype.Bool).Usage(), parser.ExpressionStart(raw), expr.MergeProperties(args[0].Properties(), args[1].Properties()))
	info, err = b.builtinOperatorRoutineInfo(info, name, 869, 869)
	if err != nil {
		return nil, err
	}
	op, _ := sqltype.NetworkSubnetKind(name)
	return functions.NewNetworkSubnet(info, op, args, namespace)
}
