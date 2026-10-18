package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The candidates are only this PG18 function family's homogeneous signatures.
// age(xid) is a transaction-identity inquiry: the caller supplies its node.
func bindDatetimeAgeFinite(b functions.Binder, raw parser.FunctionExpr, visible bool, transactionAge func(BoundExpr) (BoundExpr, error)) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]functions.Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 {
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
			names[i] = functions.NamedArgumentType(raw, i, functions.DiagnosticNameOrUnknown(o.Usage.Type))
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 {
		return signature()
	}
	prepared := functions.PrepareDatetimeAgeFinite(name, visible, raw.Variadic, operands)
	selection := functions.ResolveDatetimeAgeFinite(prepared, name, operands, functions.TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Resolution == functions.Ambiguous {
		names := func() []string {
			names := make([]string, len(operands))
			for i, o := range operands {
				names[i] = functions.DiagnosticNameOrUnknown(o.Usage.Type)
			}
			return names
		}()
		return nil, functions.ResolutionError(raw.Name, raw.Pos, names, true)
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	if selected == sqltype.Xid {
		if raw.ResolutionOnly {
			return signature()
		}
		if err := functions.ValidateCallSyntax(b, raw); err != nil {
			return nil, err
		}
		if err := functions.CallDecorationError(raw); err != nil {
			return nil, err
		}
		arg, err := b.CoerceArgument(operands[0], functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: b.Type(sqltype.Xid).Usage()})
		if err != nil {
			return nil, err
		}
		return transactionAge(arg)
	}
	e := functions.DatetimeAgeFinite{Count: len(operands)}
	properties := ExprProperties{Volatility: Immutable}
	if functions.DatetimeAgeFiniteIntrinsicStable(name, e.Count) {
		properties.Volatility = Stable
	}
	target := b.Type(selected)
	for i, o := range operands {
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
	result := functions.DatetimeAgeFiniteResult(name)
	info := b.Info(b.Type(result).Usage(), raw.Pos, properties)
	e.Info = info
	plan, err := sqltype.BindDatetimeAgeFinite(name, target, e.Usage.Type, b.Type(sqltype.Date), e.Count)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	formal := [2]sqltype.ID{selected, selected}
	info, err = b.BuiltinRoutineInfo(info, datetimeRoutineProvider, name, formal[:e.Count]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
