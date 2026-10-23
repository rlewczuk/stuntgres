package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) geometryRelationOperator(raw parser.OperatorExpr, args []expr.Expr) (expr.Expr, bool, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, false, nil
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var left sqltype.ID
	right := args[0].ResultUsage().Type.ID()
	if len(args) == 2 {
		left, right = right, args[1].ResultUsage().Type.ID()
	}
	op, ok := sqltype.GeometryRelationKind(name, left, right)
	if !ok || op.ExistingProvider() {
		return nil, false, nil
	}
	properties := args[0].Properties()
	if len(args) == 2 {
		properties = expr.MergeProperties(properties, args[1].Properties())
	}
	info, err := b.builtinOperatorRoutineInfo(b.Info(b.Type(op.ResultID()).Usage(), raw.Pos, properties), name, left, right)
	if err != nil {
		return nil, true, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	var operands [2]expr.Expr
	copy(operands[:], args)
	node, err := functions.NewGeometryRelation(info, op, operands, namespace)
	return node, true, err
}
