package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeBin(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	var small [3]Argument
	operands := small[:min(len(raw.Args), 3)]
	if len(raw.Args) > 3 && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
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
			names[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	prepared := PrepareDatetimeBin(visible, raw.Variadic, operands)
	selection := ResolveDatetimeBin(prepared, operands, TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution == Missing {
		return signature()
	}
	if selection.Unsupported != "" {
		return nil, expr.ErrorAt("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	e := DatetimeBin{}
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, o := range operands {
		id := selected
		if i == 0 {
			id = sqltype.Interval
		}
		target := b.Type(id)
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ExactFormalArgument, Target: target.Usage()})
		if err != nil {
			return nil, err
		}
		e.Args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	info := b.Info(b.Type(selected).Usage(), raw.Pos, properties)
	e.Info = info
	plan, err := sqltype.BindDatetimeBin(b.Type(sqltype.Interval), b.Type(selected), e.Usage.Type)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, "date_bin", sqltype.Interval, selected, selected)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
