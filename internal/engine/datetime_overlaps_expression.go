package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeOverlaps(b functions.Binder, raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var stack [4]functions.Argument
	operands := stack[:min(len(raw.Args), 4)]
	if len(raw.Args) > 4 && len(raw.Args) <= 100 {
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
	resolution := func(ambiguous bool) (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functions.DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return nil, functions.NotedResolutionError(b, raw.Name, raw.Pos, names, ambiguous)
	}
	prepared := functions.PrepareDatetimeOverlaps(visible, raw.Variadic, operands)
	selection := functions.ResolveDatetimeOverlaps(prepared, operands, functions.TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution != functions.Resolved {
		return resolution(selection.Resolution == functions.Ambiguous)
	}
	signature := selection.Signature
	e := functions.DatetimeOverlaps{Syntax: raw.Syntax}
	properties := ExprProperties{Volatility: Immutable}
	var local [4]bool
	for i, o := range operands {
		target := b.Type(signature[i])
		if d := o.Usage.Type; d != nil && d.BaseType().ID() == sqltype.Time && signature[i] == sqltype.Interval {
			target = b.Type(sqltype.Time)
			local[i] = true
		}
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
	plan, err := sqltype.BindDatetimeOverlaps(b.Type(signature[0]), b.Type(sqltype.Interval), b.Type(sqltype.Bool), b.Type(sqltype.Time), [2]bool{signature[1] == sqltype.Interval, signature[3] == sqltype.Interval}, local)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, Stable)
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(functions.DatetimeOverlapsResult()).Usage(), raw.Pos, properties), datetimeRoutineProvider, "overlaps", signature[:]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
