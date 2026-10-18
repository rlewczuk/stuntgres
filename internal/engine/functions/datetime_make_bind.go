package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeMake(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	labels, types, count, result := DatetimeMakeSignature(name)
	var small [7]Argument
	operands := small[:min(len(raw.Args), len(small))]
	// Bad signatures require every analyzed source type, but large calls never
	// retain executable storage. The child-analysis-before-100 rule is PG-owned.
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		operand, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
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
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	labelFailure := AnalyzeLabels(raw.ArgNames, raw.Args)
	switch labelFailure.Kind {
	case PositionalAfterNamed:
		return nil, expr.ErrorAt("42601", "positional argument cannot follow named argument", labelFailure.Pos)
	case DuplicateLabel:
		return nil, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", labelFailure.Name), labelFailure.Pos)
	}
	prepared := PrepareDatetimeMake(name, visible, raw.Variadic, operands, raw.ArgNames, &labels, &types, count, result)
	selection := ResolveDatetimeMake(prepared, operands, TemporalTypesFor(b, prepared.Demand))
	if selection.Resolution != Resolved {
		return signature()
	}
	e := DatetimeMake{Count: selection.Count, WrittenCount: selection.WrittenCount, WrittenSlots: selection.WrittenSlots, Labels: selection.Labels}
	properties := expr.Properties{Volatility: expr.Immutable}
	if DatetimeMakeIntrinsicStable(name) {
		properties.Volatility = expr.Stable
	}
	// Finalize in written order to retain unknown/parameter/coercion locations.
	for i, o := range operands {
		slot := int(e.WrittenSlots[i])
		target := b.Type(types[slot])
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ExactFormalArgument, Target: target.Usage()})
		if err != nil {
			return nil, err
		}
		e.Args[slot] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	for i := range e.Count {
		if e.Args[i] == nil {
			typ := b.Type(types[i])
			var value sqltype.Value
			var err error
			if types[i] == sqltype.Float8 {
				value, err = sqltype.Float(typ, 0)
			} else {
				value, err = sqltype.Integer(typ, 0)
			}
			if err != nil {
				return nil, err
			}
			e.Args[i] = b.Literal(value, raw.Pos)
		}
	}
	info := b.Info(b.Type(result).Usage(), raw.Pos, properties)
	e.Info = info
	plan, err := sqltype.BindDatetimeMake(name, e.Usage.Type, e.Count)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name, types[:e.Count]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
