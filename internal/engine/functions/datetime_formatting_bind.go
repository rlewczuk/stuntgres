package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeFormatting(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [4]Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: DatetimeFormattingAnalysis(name, i)})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	selection := ResolveDatetimeFormatting(name, visible, raw.Variadic, operands)
	if selection.Resolution == Missing {
		return signature()
	}
	if selection.Resolution == Ambiguous {
		return nil, ResolutionError(raw.Name, raw.Pos, []string{"unknown", DiagnosticNameOrUnknown(operands[1].Usage.Type)}, true)
	}
	if selection.Unsupported != "" {
		return nil, expr.ErrorAt("0A000", selection.Unsupported, raw.Pos)
	}
	source := b.Type(selection.Source)
	if selection.Numeric {
		return NumericFormattingOperands(b, raw, operands, source)
	}
	var args [2]expr.Expr
	properties := expr.IntrinsicProperties(DatetimeFormattingIntrinsicRule())
	for i, o := range operands {
		target := b.Type(sqltype.Text)
		if i == 0 {
			target = source
		}
		argument, err := b.CoerceArgument(o, CoercionRequest{Intent: TextFormalArgument, Target: target.Usage()})
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
	result := b.Type(DatetimeFormattingResult(name)).Usage()
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
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name, selection.Source, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return DatetimeFormatting{Info: info, Plan: plan, Args: args}, nil
}
