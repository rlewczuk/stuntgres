package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func circleOperand(d *sqltype.Descriptor) bool {
	return d != nil && d.BaseType().ID() == sqltype.Circle
}

func (b *Binder) circleOperator(raw parser.OperatorExpr, args [2]expr.Expr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	op, ok := sqltype.CircleKind(name)
	if !ok || op == sqltype.CircleRadius {
		return nil, fmt.Errorf("stuntgres: invalid circle operator")
	}
	properties := expr.MergeProperties(args[0].Properties(), args[1].Properties())
	info, err := b.builtinOperatorRoutineInfo(b.Info(b.Type(op.ResultID()).Usage(), raw.Pos, properties), name, sqltype.Circle, sqltype.Circle)
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return functions.NewCircle(info, op, args, namespace, false)
}

// The shared-spelling path has already analyzed each child. Resolve its actual
// catalog signature before selecting the dedicated provider; never rebind it.
func (b *Binder) finishCircleOperator(raw parser.OperatorExpr, operands [2]ComparisonOperand) (expr.Expr, error) {
	set, err := b.operatorCandidates(raw.Name, false)
	if err != nil {
		return nil, err
	}
	d, err := b.selectCatalogOperator(set, raw, operands[0].descriptor(), operands[1].descriptor(), false)
	if err != nil {
		return nil, err
	}
	if !routine.OperatorSeeded(d) || d.Left != 718 || d.Right != 718 {
		return nil, fmt.Errorf("stuntgres: invalid selected circle signature")
	}
	var args [2]expr.Expr
	for i, operand := range operands {
		args[i], err = b.CoerceConditional(operand, b.Type(sqltype.Circle).Usage(), raw.Name.Parts[len(raw.Name.Parts)-1].Text)
		if err != nil {
			return nil, err
		}
	}
	return b.circleOperator(raw, args)
}
