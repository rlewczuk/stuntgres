package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The candidates are only this PG18 function family's homogeneous signatures.
func BindDatetimeAgeFinite(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 {
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
			names[i] = NamedArgumentType(raw, i, DiagnosticNameOrUnknown(o.Usage.Type))
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 {
		return signature()
	}
	prepared := PrepareDatetimeAgeFinite(name, visible, raw.Variadic, operands)
	selection := ResolveDatetimeAgeFinite(prepared, name, operands, TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution == Missing {
		return signature()
	}
	if selection.Resolution == Ambiguous {
		names := func() []string {
			names := make([]string, len(operands))
			for i, o := range operands {
				names[i] = DiagnosticNameOrUnknown(o.Usage.Type)
			}
			return names
		}()
		return nil, ResolutionError(raw.Name, raw.Pos, names, true)
	}
	if selection.Unsupported != "" {
		return nil, expr.ErrorAt("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	if selected == sqltype.Xid {
		if raw.ResolutionOnly {
			return signature()
		}
		if err := ValidateCallSyntax(b, raw); err != nil {
			return nil, err
		}
		if err := CallDecorationError(raw); err != nil {
			return nil, err
		}
		arg, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: ExactFormalArgument, Target: b.Type(sqltype.Xid).Usage()})
		if err != nil {
			return nil, err
		}
		return identityExpression(b, raw, identityAge, false, arg)
	}
	e := DatetimeAgeFinite{Count: len(operands)}
	properties := expr.Properties{Volatility: expr.Immutable}
	if DatetimeAgeFiniteIntrinsicStable(name, e.Count) {
		properties.Volatility = expr.Stable
	}
	target := b.Type(selected)
	for i, o := range operands {
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
	result := DatetimeAgeFiniteResult(name)
	info := b.Info(b.Type(result).Usage(), raw.Pos, properties)
	e.Info = info
	plan, err := sqltype.BindDatetimeAgeFinite(name, target, e.Usage.Type, b.Type(sqltype.Date), e.Count)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	formal := [2]sqltype.ID{selected, selected}
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name, formal[:e.Count]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
