package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindDatetimeOverlaps(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var stack [4]functions.Argument[BoundExpr]
	operands := stack[:min(len(raw.Args), 4)]
	if len(raw.Args) > 4 && len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
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
			names[i] = datetimeFormattingTypeName(o.Usage.Type)
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, ambiguous)
	}
	prepared := functions.PrepareDatetimeOverlaps(visible, raw.Variadic, operands)
	selection := functions.ResolveDatetimeOverlaps(prepared, operands, b.temporalTypesForDemand(prepared.Demand))
	if selection.Resolution != functions.Resolved {
		return resolution(selection.Resolution == functions.Ambiguous)
	}
	signature := selection.Signature
	e := functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]{Syntax: raw.Syntax}
	properties := ExprProperties{Volatility: Immutable}
	var local [4]bool
	for i, o := range operands {
		target := b.typ(signature[i])
		if d := o.Usage.Type; d != nil && d.BaseType().ID() == sqltype.Time && signature[i] == sqltype.Interval {
			target = b.typ(sqltype.Time)
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
	plan, err := sqltype.BindDatetimeOverlaps(b.typ(signature[0]), b.typ(sqltype.Interval), b.typ(sqltype.Bool), b.typ(sqltype.Time), [2]bool{signature[1] == sqltype.Interval, signature[3] == sqltype.Interval}, local)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, Stable)
	}
	info, err := b.builtinRoutineInfo(b.info(b.typ(functions.DatetimeOverlapsResult()).Usage(), raw.Pos, properties), datetimeRoutineProvider, "overlaps", signature[:]...)
	if err != nil {
		return nil, err
	}
	e.Info = functionInfo(info)
	e.WrapperContext = !overlapsInlineSafe(e)
	return e, nil
}
