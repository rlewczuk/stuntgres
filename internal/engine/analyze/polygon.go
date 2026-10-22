package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func polygonOperand(d *sqltype.Descriptor) bool {
	return d != nil && d.BaseType().ID() == sqltype.Polygon
}
func (b *Binder) polygonOperator(raw parser.OperatorExpr, args [2]expr.Expr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	op, ok := sqltype.PolygonKind(name)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid polygon operator")
	}
	left, right := op.Signature()
	properties := expr.MergeProperties(args[0].Properties(), args[1].Properties())
	info, err := b.builtinOperatorRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), name, left, right)
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return functions.NewPolygon(info, op, args, namespace, false)
}
func (b *Binder) finishPolygonOperator(raw parser.OperatorExpr, operands [2]ComparisonOperand) (expr.Expr, error) {
	set, err := b.operatorCandidates(raw.Name, false)
	if err != nil {
		return nil, err
	}
	d, err := b.selectCatalogOperator(set, raw, operands[0].descriptor(), operands[1].descriptor(), false)
	if err != nil {
		return nil, err
	}
	op, ok := sqltype.PolygonKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	left, right := op.Signature()
	if !ok || !routine.OperatorSeeded(d) || d.Left != ids.OID(left) || d.Right != ids.OID(right) {
		return nil, fmt.Errorf("stuntgres: invalid selected polygon signature")
	}
	var args [2]expr.Expr
	for i, target := range [2]sqltype.ID{left, right} {
		args[i], err = b.CoerceConditional(operands[i], b.Type(target).Usage(), op.Name())
		if err != nil {
			return nil, err
		}
	}
	return b.polygonOperator(raw, args)
}
