package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type datetimeJustifyExpression struct {
	expressionInfo
	plan sqltype.DatetimeJustifyPlan
	arg  BoundExpr
}

func (e datetimeJustifyExpression) children() []BoundExpr { return []BoundExpr{e.arg} }
func (e datetimeJustifyExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if randomLiteralNull(e.arg) {
		return sqltype.Null(e.usage.Type), nil
	}
	value, err := e.arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.plan.Evaluate(c.Context, value)
}

func (b *expressionBinder) bindDatetimeJustify(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [1]comparisonOperand
	operands := small[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if !visible || raw.Variadic || len(operands) != 1 {
		return signature()
	}
	target := b.typ(sqltype.Interval)
	o := operands[0]
	if d := o.descriptor(); d != nil {
		if d.BaseType().ID() == sqltype.Time {
			return nil, expressionError("0A000", "time to interval conversion is unsupported", raw.Pos)
		}
		if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), target.Usage(), sqltype.Implicit); err != nil {
			return signature()
		}
	}
	arg, err := b.finalizeOperand(o, target)
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target.Usage() {
		plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target.Usage(), sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		arg = coercionExpression{b.info(target.Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit}
	}
	plan, err := sqltype.BindDatetimeJustify(raw.Name.Parts[len(raw.Name.Parts)-1].Text, target, target)
	if err != nil {
		return nil, err
	}
	return datetimeJustifyExpression{b.info(target.Usage(), raw.Pos, arg.Properties()), plan, arg}, nil
}
