package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindDatetimeExtract(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]functions.Argument[BoundExpr]
	operands := small[:min(len(raw.Args), 2)]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 {
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
	diagnosticName := raw.Name
	if raw.Syntax == parser.SQLFunctionSyntax {
		diagnosticName = parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: raw.Pos}, {Text: "extract", Pos: raw.Pos}}}
	}
	names := func() []string {
		result := make([]string, len(operands))
		for i, o := range operands {
			result[i] = datetimeFormattingTypeName(o.Usage.Type)
		}
		return result
	}
	signature := func() (BoundExpr, error) {
		return nil, b.functionSignatureErrorForTypes(diagnosticName, raw.Pos, names())
	}
	prepared := functions.PrepareDatetimeExtract(visible, raw.Variadic, operands)
	selection := functions.ResolveDatetimeExtract(prepared, operands, b.temporalTypesForDemand(prepared.Demand))
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Resolution == functions.Ambiguous {
		names := names()
		err := expressionError("42725", fmt.Sprintf("function %s(%s) is not unique", objectNameText(diagnosticName), strings.Join(names, ", ")), raw.Pos).(*sqlstate.Error)
		err.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, err
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	e := functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]{Syntax: raw.Syntax}
	if raw.Syntax == parser.SQLFunctionSyntax {
		literal, ok := raw.Args[0].(parser.LiteralExpr)
		if !ok {
			return nil, fmt.Errorf("invalid extraction syntax field")
		}
		e.Field = literal.Value.Text
	}
	properties := ExprProperties{Volatility: Immutable}
	if functions.DatetimeExtractIntrinsicStable(selected) {
		properties.Volatility = Stable
	}
	for i, o := range operands {
		id := sqltype.Text
		if i == 1 {
			id = selected
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
	// Bound parameters can become constants in PostgreSQL's custom plan. A
	// row/stage-dependent or nonimmutable argument instead permits SQL inlining;
	// that distinction applies to both arguments, not only the DATE source.
	e.DateWrapperContext = functions.DatetimeExtractWrapperContext(name, selected, properties.Volatility == Immutable && !properties.RowDependent && !properties.StageDependent)
	result := functions.DatetimeExtractResult(name)
	e.Info = functionInfo(b.info(b.typ(result).Usage(), raw.Pos, properties))
	plan, err := sqltype.BindDatetimeExtract(name, b.typ(sqltype.Text), b.typ(selected), e.Usage.Type, b.typ(sqltype.Timestamp))
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	return e, nil
}
