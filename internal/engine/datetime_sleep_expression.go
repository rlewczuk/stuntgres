package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionSleepClock(usage sqltype.Usage) (sqltype.Value, error) {
	input := c.Input
	input.TransactionStart = c.FunctionWallTime()
	return sqltype.CurrentDateTime(usage, input)
}
func (c *EvalContext) FunctionSleepArgumentPresent(arg BoundExpr) bool { return arg != nil }

func (b *expressionBinder) bindDatetimeSleep(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [1]functions.Argument[BoundExpr]
	operands := small[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := functions.AnalyzeDatetimeSleepArgument(b, child)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() error {
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(operand.Usage.Type))
		}
		return functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind, id := functions.DatetimeSleepDefinition(name)
	if !functions.DatetimeSleepShape(raw, visible, kind) {
		return nil, signature()
	}
	usage := b.typ(id).Usage()
	if !functions.DatetimeSleepCompatible(operands[0].Usage, usage) {
		return nil, signature()
	}
	if decoration := functions.DatetimeSleepDecoration(raw); decoration != "" {
		if decoration == "(*)" {
			return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
		}
		return nil, expressionError("42809", decoration+" specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	arg, err := functions.CoerceDatetimeSleepArgument(b, operands[0], usage)
	if err != nil {
		return nil, err
	}
	properties := mergeExpressionProperties(functionIntrinsicProperties(functions.DatetimeSleepIntrinsicRule()), arg.Properties())
	clockUsage := b.typ(sqltype.Timestamptz).Usage()
	info := b.info(b.typ(sqltype.Void).Usage(), raw.Pos, properties)
	return functions.NewDatetimeSleep[BoundExpr, *EvalContext](functionInfo(info), kind, arg, clockUsage)
}
