package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindDatetimeFormatting(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [4]functions.Argument[BoundExpr]
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.DatetimeFormattingAnalysis(name, i)})
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
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	selection := functions.ResolveDatetimeFormatting(name, visible, raw.Variadic, operands)
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Resolution == functions.Ambiguous {
		err := expressionError("42725", fmt.Sprintf("function %s(unknown, %s) is not unique", objectNameText(raw.Name), datetimeFormattingTypeName(operands[1].Usage.Type)), raw.Pos).(*sqlstate.Error)
		err.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, err
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	source := b.typ(selection.Source)
	if selection.Numeric {
		return b.numericFormattingOperands(raw, operands, source)
	}
	var args [2]BoundExpr
	properties := functionIntrinsicProperties(functions.DatetimeFormattingIntrinsicRule())
	for i, o := range operands {
		target := b.typ(sqltype.Text)
		if i == 0 {
			target = source
		}
		argument, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.TextFormalArgument, Target: target.Usage()})
		if err != nil {
			return nil, err
		}
		args[i] = argument
		p := argument.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	result := b.typ(functions.DatetimeFormattingResult(name)).Usage()
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
	info, err = b.builtinRoutineInfo(info, datetimeRoutineProvider, name, selection.Source, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return functions.DatetimeFormatting[BoundExpr, ExprContext, ExprProperties]{Info: functionInfo(info), Plan: plan, Args: args}, nil
}
func datetimeFormattingTypeName(d *sqltype.Descriptor) string {
	if d != nil {
		return d.DiagnosticName()
	}
	return "unknown"
}

func (b *expressionBinder) temporalTypesForDemand(demand functions.TemporalDemand) functions.TemporalTypes {
	var types functions.TemporalTypes
	if demand == 0 {
		return types
	}
	if demand&functions.NeedText != 0 {
		types.Text = b.typ(sqltype.Text).Usage()
	}
	if demand&functions.NeedDate != 0 {
		types.Date = b.typ(sqltype.Date).Usage()
	}
	if demand&functions.NeedTime != 0 {
		types.Time = b.typ(sqltype.Time).Usage()
	}
	if demand&functions.NeedTimetz != 0 {
		types.Timetz = b.typ(sqltype.Timetz).Usage()
	}
	if demand&functions.NeedTimestamp != 0 {
		types.Timestamp = b.typ(sqltype.Timestamp).Usage()
	}
	if demand&functions.NeedTimestamptz != 0 {
		types.Timestamptz = b.typ(sqltype.Timestamptz).Usage()
	}
	if demand&functions.NeedInterval != 0 {
		types.Interval = b.typ(sqltype.Interval).Usage()
	}
	if demand&functions.NeedInt4 != 0 {
		types.Int4 = b.typ(sqltype.Int4).Usage()
	}
	if demand&functions.NeedFloat8 != 0 {
		types.Float8 = b.typ(sqltype.Float8).Usage()
	}
	return types
}
