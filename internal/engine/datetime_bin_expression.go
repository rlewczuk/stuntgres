package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindDatetimeBin(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
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
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o.Usage.Type)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	prepared := functions.PrepareDatetimeBin(visible, raw.Variadic, operands)
	selection := functions.ResolveDatetimeBin(prepared, operands, b.temporalTypesForDemand(prepared.Demand))
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	e := functions.DatetimeBin{}
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		id := selected
		if i == 0 {
			id = sqltype.Interval
		}
		target := b.typ(id)
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
	info := b.info(b.typ(selected).Usage(), raw.Pos, properties)
	e.Info = info
	plan, err := sqltype.BindDatetimeBin(b.typ(sqltype.Interval), b.typ(selected), e.Usage.Type)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	info, err = b.builtinRoutineInfo(info, datetimeRoutineProvider, "date_bin", sqltype.Interval, selected, selected)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
