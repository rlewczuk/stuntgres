package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type datetimeFormattingExpression struct {
	expressionInfo
	plan sqltype.DatetimeFormatPlan
	args [2]BoundExpr
}

func (e datetimeFormattingExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.args {
		if randomLiteralNull(arg) {
			return sqltype.Null(e.usage.Type), nil
		}
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
func (b *expressionBinder) bindDatetimeFormatting(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [4]comparisonOperand
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.stringOperand(arg, name != "to_char" || i > 0)
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
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	numeric := func(id sqltype.ID) bool {
		return id == sqltype.Int2 || id == sqltype.Int4 || id == sqltype.Int8 || id == sqltype.Numeric || id == sqltype.Float4 || id == sqltype.Float8
	}
	if visible && !raw.Variadic && name == "to_timestamp" && len(operands) == 1 {
		d := operands[0].descriptor()
		if d == nil || numeric(d.BaseType().ID()) {
			return nil, expressionError("0A000", "epoch to_timestamp is unsupported", raw.Pos)
		}
	}
	if !visible || len(operands) != 2 || raw.Variadic {
		return signature()
	}
	for i, o := range operands {
		if name == "to_char" && i == 0 {
			continue
		}
		if d := o.descriptor(); d != nil && !sqltype.StringTextInput(d.BaseType().ID()) {
			return signature()
		}
	}
	source := b.typ(sqltype.Text)
	if name == "to_char" {
		d := operands[0].descriptor()
		if d == nil {
			err := expressionError("42725", fmt.Sprintf("function %s(unknown, %s) is not unique", objectNameText(raw.Name), datetimeFormattingTypeName(operands[1])), raw.Pos).(*sqlstate.Error)
			err.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
			return nil, err
		}
		id := d.BaseType().ID()
		if numeric(id) {
			if id == sqltype.Int2 {
				id = sqltype.Float8
			}
			return b.numericFormattingOperands(raw, operands, b.typ(id))
		}
		switch id {
		case sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval:
			source = b.typ(id)
		case sqltype.Date:
			source = b.typ(sqltype.Timestamptz)
		case sqltype.Time:
			return nil, expressionError("0A000", "time to interval conversion is unsupported", raw.Pos)
		default:
			return signature()
		}
	}
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
		targetUsage := target.Usage()
		if target.ID() == sqltype.Text && argument.ResultUsage().Collation.ID != 0 {
			targetUsage.Collation = argument.ResultUsage().Collation
		}
		if argument.ResultUsage() != targetUsage {
			plan, err := sqltype.PlanCoercion(argument.ResultUsage(), targetUsage, sqltype.Implicit)
			if argument.ResultUsage().Type.BaseType().ID() == sqltype.Char {
				plan, err = sqltype.PlanExtremaCharText(argument.ResultUsage(), targetUsage)
			}
			if err != nil {
				return nil, err
			}
			info := b.info(targetUsage, argument.SourcePosition(), coercionProperties(argument, plan))
			if target.ID() == sqltype.Text {
				info, err = b.coercionInfo(info, argument)
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
	if name == "to_date" {
		result = b.typ(sqltype.Date).Usage()
	} else if name == "to_timestamp" {
		result = b.typ(sqltype.Timestamptz).Usage()
	}
	info := b.info(result, raw.Pos, properties)
	collated, err := b.collatedInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, properties), args[:]...)
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
	plan, err := sqltype.BindDatetimeFormat(b.ctx, name, info.usage, args[0].ResultUsage(), constant)
	if err != nil {
		return nil, err
	}
	plan = plan.WithCollationConflict(collated.collationConflict)
	return datetimeFormattingExpression{info, plan, args}, nil
}
func datetimeFormattingTypeName(o comparisonOperand) string {
	if d := o.descriptor(); d != nil {
		return d.DiagnosticName()
	}
	return "unknown"
}
