package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) geometryRelationOperator(raw parser.OperatorExpr, args []expr.Expr) (expr.Expr, bool, error) {
	if len(args) != 2 {
		return nil, false, nil
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	left, right := args[0].ResultUsage().Type.ID(), args[1].ResultUsage().Type.ID()
	op, ok := sqltype.GeometryRelationKind(name, left, right)
	if !ok || op.ExistingProvider() {
		return nil, false, nil
	}
	properties := expr.MergeProperties(args[0].Properties(), args[1].Properties())
	info, err := b.builtinOperatorRoutineInfo(b.Info(b.Type(op.ResultID()).Usage(), raw.Pos, properties), name, left, right)
	if err != nil {
		return nil, true, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	node, err := functions.NewGeometryRelation(info, op, [2]expr.Expr{args[0], args[1]}, namespace)
	return node, true, err
}
