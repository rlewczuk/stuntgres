package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func functionNamedType(raw parser.FunctionExpr, index int, typ string) string {
	if len(raw.ArgNames) > index && raw.ArgNames[index].Text != "" {
		return raw.ArgNames[index].Text + " => " + typ
	}
	return typ
}

func (b *expressionBinder) bindDatetimeMake(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	labels, types, count, result := functions.DatetimeMakeSignature(name)
	var small [7]functions.Argument
	operands := small[:min(len(raw.Args), len(small))]
	// Bad signatures require every analyzed source type, but large calls never
	// retain executable storage. The child-analysis-before-100 rule is PG-owned.
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		operand, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(o.Usage.Type))
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	labelFailure := functions.AnalyzeLabels(raw.ArgNames, raw.Args)
	switch labelFailure.Kind {
	case functions.PositionalAfterNamed:
		return nil, expressionError("42601", "positional argument cannot follow named argument", labelFailure.Pos)
	case functions.DuplicateLabel:
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", labelFailure.Name), labelFailure.Pos)
	}
	prepared := functions.PrepareDatetimeMake(name, visible, raw.Variadic, operands, raw.ArgNames, &labels, &types, count, result)
	selection := functions.ResolveDatetimeMake(prepared, operands, b.temporalTypesForDemand(prepared.Demand))
	if selection.Resolution != functions.Resolved {
		return signature()
	}
	e := functions.DatetimeMake{Count: selection.Count, WrittenCount: selection.WrittenCount, WrittenSlots: selection.WrittenSlots, Labels: selection.Labels}
	properties := ExprProperties{Volatility: Immutable}
	if functions.DatetimeMakeIntrinsicStable(name) {
		properties.Volatility = Stable
	}
	// Finalize in written order to retain unknown/parameter/coercion locations.
	for i, o := range operands {
		slot := int(e.WrittenSlots[i])
		target := b.typ(types[slot])
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: target.Usage()})
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
			typ := b.typ(types[i])
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
			e.Args[i] = literalExpression{b.info(typ.Usage(), raw.Pos, ExprProperties{Volatility: Immutable}), value}
		}
	}
	info := b.info(b.typ(result).Usage(), raw.Pos, properties)
	e.Info = functionInfo(info)
	plan, err := sqltype.BindDatetimeMake(name, e.Usage.Type, e.Count)
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	info, err = b.builtinRoutineInfo(info, datetimeRoutineProvider, name, types[:e.Count]...)
	if err != nil {
		return nil, err
	}
	e.Info = functionInfo(info)
	return e, nil
}

// Definition/presentation paths retain written labels/order and omitted defaults;
// execution and dependency walkers use declared slots, never this display order.

// Definition/presentation paths retain written labels/order and omitted defaults;
// execution and dependency walkers use declared slots, never this display order.
func datetimeMakeSQL(e functions.DatetimeMake, name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	if e.Count < 1 || e.Count > 7 || e.WrittenCount < 0 || e.WrittenCount > e.Count {
		return "", fmt.Errorf("invalid stored datetime constructor")
	}
	parts := make([]string, e.WrittenCount)
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= e.Count || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored constructor argument")
		}
		text, err := render(e.Args[slot])
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
