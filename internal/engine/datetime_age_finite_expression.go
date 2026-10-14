package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The candidates are only this PG18 function family's homogeneous signatures.
// XID is diagnostic-only for age: scalar admission does not add an age overload.
func (b *expressionBinder) bindDatetimeAgeFinite(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]functions.Argument[BoundExpr]
	operands := small[:min(len(raw.Args), len(small))]
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
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o.Usage.Type)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	prepared := functions.PrepareDatetimeAgeFinite(name, visible, raw.Variadic, operands)
	selection := functions.ResolveDatetimeAgeFinite(prepared, name, operands, b.temporalTypesForDemand(prepared.Demand))
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Resolution == functions.Ambiguous {
		names := func() []string {
			names := make([]string, len(operands))
			for i, o := range operands {
				names[i] = datetimeFormattingTypeName(o.Usage.Type)
			}
			return names
		}()
		err := expressionError("42725", fmt.Sprintf("function %s(%s) is not unique", objectNameText(raw.Name), strings.Join(names, ", ")), raw.Pos).(*sqlstate.Error)
		err.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, err
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	selected := selection.Selected
	e := functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]{Count: len(operands)}
	properties := ExprProperties{Volatility: Immutable}
	if functions.DatetimeAgeFiniteIntrinsicStable(name, e.Count) {
		properties.Volatility = Stable
	}
	target := b.typ(selected)
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
	e.Info = functionInfo(b.info(b.typ(result).Usage(), raw.Pos, properties))
	plan, err := sqltype.BindDatetimeAgeFinite(name, target, e.Usage.Type, b.typ(sqltype.Date), e.Count)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	return e, nil
}
