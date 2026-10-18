package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindDatetimeOverlaps binds OVERLAPS. The engine sets WrapperContext, which
// depends on the inline safety of the bound endpoints.
func BindDatetimeOverlaps(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	var stack [4]Argument
	operands := stack[:min(len(raw.Args), 4)]
	if len(raw.Args) > 4 && len(raw.Args) <= 100 {
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
	resolution := func(ambiguous bool) (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return nil, NotedResolutionError(b, raw.Name, raw.Pos, names, ambiguous)
	}
	prepared := PrepareDatetimeOverlaps(visible, raw.Variadic, operands)
	selection := ResolveDatetimeOverlaps(prepared, operands, TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution != Resolved {
		return resolution(selection.Resolution == Ambiguous)
	}
	signature := selection.Signature
	e := DatetimeOverlaps{Syntax: raw.Syntax}
	properties := expr.Properties{Volatility: expr.Immutable}
	var local [4]bool
	for i, o := range operands {
		target := b.Type(signature[i])
		if d := o.Usage.Type; d != nil && d.BaseType().ID() == sqltype.Time && signature[i] == sqltype.Interval {
			target = b.Type(sqltype.Time)
			local[i] = true
		}
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
	plan, err := sqltype.BindDatetimeOverlaps(b.Type(signature[0]), b.Type(sqltype.Interval), b.Type(sqltype.Bool), b.Type(sqltype.Time), [2]bool{signature[1] == sqltype.Interval, signature[3] == sqltype.Interval}, local)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, expr.Stable)
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(DatetimeOverlapsResult()).Usage(), raw.Pos, properties), DatetimeRoutineProvider, "overlaps", signature[:]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
