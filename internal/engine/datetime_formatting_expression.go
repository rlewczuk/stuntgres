package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeFormatting(b functions.Binder, raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [4]functions.Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
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
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	selection := functions.ResolveDatetimeFormatting(name, visible, raw.Variadic, operands)
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Resolution == functions.Ambiguous {
		return nil, functions.ResolutionError(raw.Name, raw.Pos, []string{"unknown", functions.DiagnosticNameOrUnknown(operands[1].Usage.Type)}, true)
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	source := b.Type(selection.Source)
	if selection.Numeric {
		return functions.NumericFormattingOperands(b, raw, operands, source)
	}
	var args [2]BoundExpr
	properties := expr.IntrinsicProperties(functions.DatetimeFormattingIntrinsicRule())
	for i, o := range operands {
		target := b.Type(sqltype.Text)
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
	result := b.Type(functions.DatetimeFormattingResult(name)).Usage()
	info := b.Info(result, raw.Pos, properties)
	collated, err := b.CollatedInfo(b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties), args[:]...)
	if err != nil {
		return nil, err
	}
	if result.Type.ID() == sqltype.Text {
		info = collated
	}
	var constant *string
	if value, ok := b.ConstantValue(args[1]); ok && !value.IsNull() {
		text, err := value.Text()
		if err != nil {
			return nil, err
		}
		constant = &text
	}
	plan, err := sqltype.BindDatetimeFormat(b.Context(), name, info.Usage, args[0].ResultUsage(), constant)
	if err != nil {
		return nil, err
	}
	plan = plan.WithCollationConflict(collated.CollationConflict)
	info, err = b.BuiltinRoutineInfo(info, datetimeRoutineProvider, name, selection.Source, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return functions.DatetimeFormatting{Info: info, Plan: plan, Args: args}, nil
}
