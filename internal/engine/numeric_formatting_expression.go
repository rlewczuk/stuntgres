package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type numericFormattingExpression struct {
	expressionInfo
	plan sqltype.NumericFormatPlan
	args [2]BoundExpr
}

func (e numericFormattingExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.plan.Evaluate(c.Context, values[0], values[1], c.Input, c.Output)
}
func (b *expressionBinder) bindNumericFormatting(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [4]comparisonOperand
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.stringOperand(arg, true)
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
	if !visible || len(operands) != 2 || raw.Variadic {
		return nil, b.numericFormattingSignature(raw, operands)
	}
	for _, o := range operands {
		if d := o.descriptor(); d != nil && !sqltype.StringTextInput(d.BaseType().ID()) {
			return nil, b.numericFormattingSignature(raw, operands)
		}
	}
	return b.numericFormattingOperands(raw, operands, b.typ(sqltype.Text))
}
func (b *expressionBinder) numericFormattingSignature(raw parser.FunctionExpr, operands []comparisonOperand) error {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = datetimeFormattingTypeName(o)
	}
	return functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
}

// Numeric to_char arrives with already-transformed operands from the temporal
// resolver, retaining argument error precedence and parameter occurrences.
func (b *expressionBinder) numericFormattingOperands(raw parser.FunctionExpr, operands []comparisonOperand, source *sqltype.Descriptor) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var args [2]BoundExpr
	properties := ExprProperties{Volatility: Stable}
	for i, o := range operands {
		target := b.typ(sqltype.Text)
		if i == 0 {
			target = source
		}
		argument, err := b.finalizeOperand(o, target)
		if err != nil {
			return nil, err
		}
		usage := target.Usage()
		if target.ID() == sqltype.Text && argument.ResultUsage().Collation.ID != 0 {
			usage.Collation = argument.ResultUsage().Collation
		}
		if argument.ResultUsage() != usage {
			plan, err := sqltype.PlanCoercion(argument.ResultUsage(), usage, sqltype.Implicit)
			if argument.ResultUsage().Type.BaseType().ID() == sqltype.Char {
				plan, err = sqltype.PlanExtremaCharText(argument.ResultUsage(), usage)
			}
			if err != nil {
				return nil, err
			}
			info := b.info(usage, argument.SourcePosition(), coercionProperties(argument, plan))
			if target.ID() == sqltype.Text {
				info, err = collatedInfo(info, argument)
				if err != nil {
					return nil, err
				}
			}
			argument = coercionExpression{info, argument, plan, sqltype.Implicit}
		}
		args[i] = argument
		p := argument.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	result := b.typ(sqltype.Text).Usage()
	if name == "to_number" {
		result = b.typ(sqltype.Numeric).Usage()
	}
	info := b.info(result, raw.Pos, properties)
	collated, err := collatedInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, properties), args[:]...)
	if err != nil {
		return nil, err
	}
	if result.Type.ID() == sqltype.Text {
		info = collated
	}
	var constant *string
	if literal, ok := args[1].(literalExpression); ok && !literal.value.IsNull() {
		text, err := literal.value.Text()
		if err != nil {
			return nil, err
		}
		constant = &text
	}
	plan, err := sqltype.BindNumericFormat(b.ctx, name, info.usage, args[0].ResultUsage(), constant)
	if err != nil {
		return nil, err
	}
	return numericFormattingExpression{info, plan, args}, nil
}
