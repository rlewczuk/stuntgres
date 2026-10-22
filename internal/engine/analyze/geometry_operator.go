package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindBoxSame(raw parser.OperatorExpr) (expr.Expr, error) {
	operands := make([]ComparisonOperand, len(raw.Args))
	var left, right *sqltype.Descriptor
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
		if i == 0 {
			left = o.descriptor()
		} else {
			right = o.descriptor()
		}
	}
	if len(operands) != 2 || left != nil && left.BaseType().ID() != sqltype.Box || right != nil && right.BaseType().ID() != sqltype.Box {
		return nil, functions.OperatorError(sqlError("42883", "incompatible operands"), left, right, raw)
	}
	args := make([]expr.Expr, 2)
	properties := expr.Properties{}
	for i, o := range operands {
		node, err := b.CoerceConditional(o, b.Type(sqltype.Box).Usage(), "~=")
		if err != nil {
			return nil, err
		}
		args[i] = node
		properties = expr.MergeProperties(properties, node.Properties())
	}
	info, err := b.BuiltinOperatorRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), "~=", sqltype.Box, sqltype.Box)
	if err != nil {
		return nil, err
	}
	return functions.NewBinary(info, "~=", args, parser.OrdinaryFunctionSyntax)
}
