package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func pointOperand(d *sqltype.Descriptor) bool {
	return d != nil && d.BaseType().ID() == sqltype.Point
}

func (b *Binder) pointOperator(raw parser.OperatorExpr, args [2]expr.Expr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	op, ok := sqltype.PointKind(name)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid point operator")
	}
	properties := expr.MergeProperties(args[0].Properties(), args[1].Properties())
	info, err := b.builtinOperatorRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), name, sqltype.Point, sqltype.Point)
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return functions.NewPoint(info, op, args, namespace, false)
}

// Children have already been analyzed. Catalog selection, not spelling, owns
// provider dispatch and UNKNOWN peer inference.
func (b *Binder) finishPointOperator(raw parser.OperatorExpr, operands [2]ComparisonOperand) (expr.Expr, error) {
	set, err := b.operatorCandidates(raw.Name, false)
	if err != nil {
		return nil, err
	}
	d, err := b.selectCatalogOperator(set, raw, operands[0].descriptor(), operands[1].descriptor(), false)
	if err != nil {
		return nil, err
	}
	if !routine.OperatorSeeded(d) || d.Left != 600 || d.Right != 600 {
		return nil, fmt.Errorf("stuntgres: invalid selected point signature")
	}
	var args [2]expr.Expr
	for i, operand := range operands {
		args[i], err = b.CoerceConditional(operand, b.Type(sqltype.Point).Usage(), raw.Name.Parts[len(raw.Name.Parts)-1].Text)
		if err != nil {
			return nil, err
		}
	}
	return b.pointOperator(raw, args)
}

// The builtin-only shared path preserves JSONB's array-element coercion while
// selecting POINT only from a known point operand. Custom/namespace competition
// still goes through BindCatalogOperator before this path is entered.
func (b *Binder) bindPointOrJSONBExistence(raw parser.OperatorExpr) (expr.Expr, error) {
	var args [2]functions.Argument
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	if pointOperand(args[0].Usage.Type) || pointOperand(args[1].Usage.Type) {
		operands := [2]ComparisonOperand{
			{Node: args[0].Expression, raw: args[0].Raw},
			{Node: args[1].Expression, raw: args[1].Raw},
		}
		return b.finishPointOperator(raw, operands)
	}
	return functions.BindJSONBExistenceOperands(b, raw, args)
}
