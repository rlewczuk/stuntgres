package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeTrunc(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
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
	names := func() []string {
		result := make([]string, len(operands))
		for i, o := range operands {
			result[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return result
	}
	signature := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names()) }
	prepared := PrepareDatetimeTrunc(visible, raw.Variadic, operands)
	selection := ResolveDatetimeTrunc(prepared, operands, TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution == Missing {
		return signature()
	}
	if selection.Resolution == Ambiguous {
		return nil, ResolutionError(raw.Name, raw.Pos, names(), true)
	}
	if selection.Unsupported != "" {
		return nil, expr.ErrorAt("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	input := selection.Input
	e := DatetimeTrunc{Count: len(operands)}
	properties := expr.Properties{Volatility: expr.Immutable}
	if DatetimeTruncIntrinsicStable(selected, e.Count) {
		properties.Volatility = expr.Stable
	}
	for i, o := range operands {
		id := sqltype.Text
		if i == 1 {
			id = input
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
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, "date_trunc", formal[:e.Count]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
