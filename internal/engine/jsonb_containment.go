package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonbContainmentName(name string) bool { return name == "@>" || name == "<@" }

// Operands have already been analyzed and operator visibility checked by the
// shared array/JSONB spelling dispatcher. Do not analyze occurrences twice.
func (b *expressionBinder) bindJSONBContainment(raw parser.OperatorExpr, operands [2]comparisonOperand) (expr.Expr, error) {
	target := b.typ(sqltype.JSONB).Usage()
	for _, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), target, sqltype.Implicit); err != nil {
				return nil, b.arrayMembershipError(operands[0].descriptor(), operands[1].descriptor(), raw)
			}
		}
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args := make([]expr.Expr, 2)
	properties := expr.Properties{}
	for i, o := range operands {
		arg, err := b.coerceConditional(o, target, name)
		if err != nil {
			return nil, err
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info := b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties)
	node, err := functions.NewBinary(info, name, args, parser.OrdinaryFunctionSyntax)
	if err != nil {
		return nil, err
	}
	info, err = b.builtinOperatorRoutineInfo(info, name, sqltype.JSONB, sqltype.JSONB)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
