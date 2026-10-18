package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeExtract(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]Argument
	operands := small[:min(len(raw.Args), 2)]
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
	diagnosticName := raw.Name
	if raw.Syntax == parser.SQLFunctionSyntax {
		diagnosticName = parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: raw.Pos}, {Text: "extract", Pos: raw.Pos}}}
	}
	names := func() []string {
		result := make([]string, len(operands))
		for i, o := range operands {
			result[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return result
	}
	signature := func() (expr.Expr, error) {
		return nil, b.SignatureError(diagnosticName, raw.Pos, names())
	}
	prepared := PrepareDatetimeExtract(visible, raw.Variadic, operands)
	selection := ResolveDatetimeExtract(prepared, operands, TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution == Missing {
		return signature()
	}
	if selection.Resolution == Ambiguous {
		return nil, ResolutionError(diagnosticName, raw.Pos, names(), true)
	}
	if selection.Unsupported != "" {
		return nil, expr.ErrorAt("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	e := DatetimeExtract{Syntax: raw.Syntax}
	if raw.Syntax == parser.SQLFunctionSyntax {
		literal, ok := raw.Args[0].(parser.LiteralExpr)
		if !ok {
			return nil, fmt.Errorf("invalid extraction syntax field")
		}
		e.Field = literal.Value.Text
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	if DatetimeExtractIntrinsicStable(selected) {
		properties.Volatility = expr.Stable
	}
	for i, o := range operands {
		id := sqltype.Text
		if i == 1 {
			id = selected
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
	// Bound parameters can become constants in PostgreSQL's custom plan. A
	// row/stage-dependent or nonimmutable argument instead permits SQL inlining;
	// that distinction applies to both arguments, not only the DATE source.
	e.DateWrapperContext = DatetimeExtractWrapperContext(name, selected, properties.Volatility == expr.Immutable && !properties.RowDependent && !properties.StageDependent)
	result := DatetimeExtractResult(name)
	info := b.Info(b.Type(result).Usage(), raw.Pos, properties)
	e.Info = info
	plan, err := sqltype.BindDatetimeExtract(name, b.Type(sqltype.Text), b.Type(selected), e.Usage.Type, b.Type(sqltype.Timestamp))
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name, sqltype.Text, selected)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
