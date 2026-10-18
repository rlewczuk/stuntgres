package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) selectJSONBConcat(raw parser.OperatorExpr, operands [2]functions.Argument, visible bool) (expr.Expr, bool, error) {
	known := false
	var args [2]comparisonOperand
	for i, o := range operands {
		if o.Usage.Type != nil {
			if o.Usage.Type.BaseType().ID() != sqltype.JSONB {
				return nil, false, nil
			}
			known = true
		}
		args[i] = comparisonOperand{node: o.Expression, raw: o.Raw}
	}
	if !visible || !known {
		return nil, false, nil
	}
	node, err := b.jsonbOperatorNode(raw, args, sqltype.JSONB)
	return node, true, err
}

// Select deletion with a known JSONB left operand, or an unknown left operand
// and a known right operand implicitly coercible to TEXT. Unknown-left/integer
// stays numeric; both unknown operands retain arithmetic ambiguity.
func (b *expressionBinder) selectJSONBDelete(raw parser.OperatorExpr, left, right comparisonOperand) (expr.Expr, bool, error) {
	lt, rt := left.descriptor(), right.descriptor()
	knownLeft := lt != nil && lt.BaseType().ID() == sqltype.JSONB
	rhs := sqltype.Text
	if !knownLeft {
		if lt != nil || rt == nil {
			return nil, false, nil
		}
		if _, err := sqltype.PlanCoercion(right.node.ResultUsage(), b.typ(sqltype.Text).Usage(), sqltype.Implicit); err != nil {
			return nil, false, nil
		}
	} else if rt != nil {
		if rt.BaseType().ID() == sqltype.Int2 || rt.BaseType().ID() == sqltype.Int4 {
			rhs = sqltype.Int4
		}
		if _, err := sqltype.PlanCoercion(right.node.ResultUsage(), b.typ(rhs).Usage(), sqltype.Implicit); err != nil {
			return nil, true, functions.OperatorError(sqlError("42883", "operator does not exist"), lt, rt, raw)
		}
	}
	node, err := b.jsonbOperatorNode(raw, [2]comparisonOperand{left, right}, rhs)
	return node, true, err
}

func (b *expressionBinder) jsonbOperatorNode(raw parser.OperatorExpr, operands [2]comparisonOperand, rhs sqltype.ID) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	targets := [2]sqltype.Usage{b.typ(sqltype.JSONB).Usage(), b.typ(rhs).Usage()}
	args := make([]expr.Expr, 2)
	properties := expr.Properties{}
	for i, o := range operands {
		node, err := b.coerceConditional(o, targets[i], name)
		if err != nil {
			return nil, err
		}
		args[i] = node
		properties = expr.MergeProperties(properties, node.Properties())
	}
	info := b.info(targets[0], raw.Pos, properties)
	node, err := functions.NewBinary(info, name, args, parser.OrdinaryFunctionSyntax)
	if err != nil {
		return nil, err
	}
	info, err = b.builtinOperatorRoutineInfo(info, name, sqltype.JSONB, rhs)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
