package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeTrunc(b functions.Binder, raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [3]functions.Argument
	operands := small[:min(len(raw.Args), 3)]
	if len(raw.Args) > 3 && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
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
	names := func() []string {
		result := make([]string, len(operands))
		for i, o := range operands {
			result[i] = functions.DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return result
	}
	signature := func() (BoundExpr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names()) }
	prepared := functions.PrepareDatetimeTrunc(visible, raw.Variadic, operands)
	selection := functions.ResolveDatetimeTrunc(prepared, operands, functions.TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Resolution == functions.Ambiguous {
		return nil, functions.ResolutionError(raw.Name, raw.Pos, names(), true)
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	input := selection.Input
	e := functions.DatetimeTrunc{Count: len(operands)}
	properties := ExprProperties{Volatility: Immutable}
	if functions.DatetimeTruncIntrinsicStable(selected, e.Count) {
		properties.Volatility = Stable
	}
	for i, o := range operands {
		id := sqltype.Text
		if i == 1 {
			id = input
		}
		target := b.Type(id)
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: target.Usage()})
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
	var zone *sqltype.Descriptor
	if e.Count == 3 {
		zone = b.Type(sqltype.Text)
	}
	plan, err := sqltype.BindDatetimeTrunc(b.Type(sqltype.Text), b.Type(input), e.Usage.Type, zone)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	formal := [3]sqltype.ID{sqltype.Text, selected, sqltype.Text}
	info, err = b.BuiltinRoutineInfo(info, datetimeRoutineProvider, "date_trunc", formal[:e.Count]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
