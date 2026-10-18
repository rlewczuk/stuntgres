package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindFormat(raw parser.FunctionExpr) (BoundExpr, error) {
	var args []BoundExpr
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
	}
	var first functions.Argument
	var rawUnknown [2]uint64
	for i, r := range raw.Args {
		operand, err := b.AnalyzeArgument(r, functions.FormatArgumentAnalysis(i))
		node := operand.Expression
		if i == 0 {
			first = operand
		} else if err == nil {
			if functions.FormatUnknownArgument(r, i) {
				rawUnknown[i/64] |= uint64(1) << uint(i%64)
			}
			if parameter, ok := node.(parameterExpression); ok && parameter.Usage.Type == nil {
				b.anyParameters = append(b.anyParameters, parameter)
			}
		}
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = node
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if functions.ResolveFormat(visible, raw.Variadic, len(args), first.Usage) != functions.Resolved {
		names := make([]string, len(args))
		for i, arg := range args {
			names[i] = "unknown"
			if literal, ok := raw.Args[i].(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
				continue
			}
			if arg != nil && arg.ResultUsage().Type != nil {
				names[i] = arg.ResultUsage().Type.DiagnosticName()
			}
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	// Retain this occurrence's unresolved type until every child has been
	// analyzed and the signature selected; a later occurrence may constrain it.
	if first.Usage.Type == nil {
		args[0], err = functions.CoerceFormatFirst(b, first, b.typ(sqltype.Text).Usage())
		if err != nil {
			return nil, err
		}
	}
	if raw.Variadic && (args[1].ResultUsage().Type == nil || !args[1].ResultUsage().Type.BaseType().IsArray()) {
		return nil, expressionError("42804", "VARIADIC argument must be an array", raw.Args[1].SourcePosition())
	}
	text := b.typ(sqltype.Text).Usage()
	if request, needed := functions.FormatFirstCoercion(args[0].ResultUsage(), text); needed {
		first.Expression, first.Usage = args[0], args[0].ResultUsage()
		args[0], err = b.CoerceArgument(first, request)
		if err != nil {
			return nil, err
		}
	}
	properties := expr.IntrinsicProperties(functions.FormatIntrinsicRule())
	for _, arg := range args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
	}
	info, err := b.collatedInfo(b.info(text, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	formal, count := [2]sqltype.ID{sqltype.Text, sqltype.ID(2276)}, 1
	if raw.Variadic || len(args) > 1 {
		count = 2
	}
	info, err = b.builtinRoutineInfo(info, variadicRoutineProvider, "format", formal[:count]...)
	if err != nil {
		return nil, err
	}
	int4 := b.typ(sqltype.Int4)
	b.info(int4.Usage(), raw.Pos, ExprProperties{})
	return functions.Format{Info: info, Args: args, Variadic: raw.Variadic, Int4: int4, RawUnknown: rawUnknown}, nil
}
