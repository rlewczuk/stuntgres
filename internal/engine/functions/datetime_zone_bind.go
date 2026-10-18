package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeZone(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [3]Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ZoneArgument})
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
	resolutionError := func(ambiguous bool) (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return nil, NotedResolutionError(b, raw.Name, raw.Pos, names, ambiguous)
	}
	prepared := PrepareDatetimeZone(name, visible, raw.Variadic, operands)
	signature := ResolveDatetimeZone(prepared, name, operands, TemporalTypesFor(b, prepared.Demand))
	if signature.Resolution != Resolved {
		return resolutionError(signature.Resolution == Ambiguous)
	}
	var args [3]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	if signature.IntrinsicStable() {
		properties.Volatility = expr.Stable
	}
	for i, o := range operands {
		target := b.Type(signature.Args[i])
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ExactFormalArgument, Target: target.Usage()})
		if err != nil {
			return nil, err
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info := b.Info(b.Type(signature.Result).Usage(), raw.Pos, properties)
	var err error
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name, signature.Args[:signature.Count]...)
	if err != nil {
		return nil, err
	}
	if name == "timezone" {
		e := Timezone{Info: info, Function: true, Local: signature.Count == 1}
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
	return DatetimeZoneCalendar{Info: info, Name: name, Args: args, Count: signature.Count, Provider: provider}, nil
}

// BindTimeZone binds AT TIME ZONE and AT LOCAL as the visible timezone
// function, keeping the operator spelling and position and the conservative
// STABLE analysis contract (plannedVolatility uses the resolved overload).
func BindTimeZone(b Binder, raw parser.TimeZoneExpr) (expr.Expr, error) {
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "timezone"}}}
	args, pos := []parser.Expr{raw.Zone, raw.Value}, raw.Pos
	if raw.Local {
		args, pos = []parser.Expr{raw.Value}, parser.Position{}
	}
	node, err := bindDatetimeZone(b, parser.FunctionExpr{Name: name, Args: args, Pos: pos}, true)
	if err != nil {
		return nil, err
	}
	e := node.(Timezone)
	e.Function, e.Pos = false, raw.Pos
	e.Property.Volatility = max(e.Property.Volatility, expr.Stable)
	return e, nil
}
