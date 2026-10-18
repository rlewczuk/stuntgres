package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeZone(b functions.Binder, raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [3]functions.Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ZoneArgument})
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
	resolutionError := func(ambiguous bool) (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functions.DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return nil, functions.NotedResolutionError(b, raw.Name, raw.Pos, names, ambiguous)
	}
	prepared := functions.PrepareDatetimeZone(name, visible, raw.Variadic, operands)
	signature := functions.ResolveDatetimeZone(prepared, name, operands, functions.TemporalTypesFor(b, prepared.Demand))
	if signature.Resolution != functions.Resolved {
		return resolutionError(signature.Resolution == functions.Ambiguous)
	}
	var args [3]BoundExpr
	properties := ExprProperties{Volatility: Immutable}
	if signature.IntrinsicStable() {
		properties.Volatility = Stable
	}
	for i, o := range operands {
		target := b.Type(signature.Args[i])
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: target.Usage()})
		if err != nil {
			return nil, err
		}
		args[i] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	info := b.Info(b.Type(signature.Result).Usage(), raw.Pos, properties)
	var err error
	info, err = b.BuiltinRoutineInfo(info, datetimeRoutineProvider, name, signature.Args[:signature.Count]...)
	if err != nil {
		return nil, err
	}
	if name == "timezone" {
		e := functions.Timezone{Info: info, Function: true, Local: signature.Count == 1}
		if e.Local {
			e.Value = args[0]
		} else {
			e.Zone, e.Value = args[0], args[1]
		}
		return e, nil
	}
	op := sqltype.ArithmeticAdd
	if name == "date_subtract" {
		op = sqltype.ArithmeticSubtract
	}
	provider, err := sqltype.BindArithmetic(op, b.Type(sqltype.Timestamptz), b.Type(sqltype.Interval), b.Type(sqltype.Timestamptz))
	if err != nil {
		return nil, err
	}
	return functions.DatetimeZoneCalendar{Info: info, Name: name, Args: args, Count: signature.Count, Provider: provider}, nil
}
