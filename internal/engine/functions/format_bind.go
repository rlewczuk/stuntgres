package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindFormat runs after the engine's shared call gates (SharedGates), which
// also send other spellings of format to the resolution-only diagnostics.
// Arguments are analyzed before builtin visibility.
func bindFormat(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var args []expr.Expr
	if len(raw.Args) <= 100 {
		args = make([]expr.Expr, len(raw.Args))
	}
	var first Argument
	var rawUnknown [2]uint64
	for i, r := range raw.Args {
		operand, err := b.AnalyzeArgument(r, FormatArgumentAnalysis(i))
		node := operand.Expression
		if i == 0 {
			first = operand
		} else if err == nil {
			if FormatUnknownArgument(r, i) {
				rawUnknown[i/64] |= uint64(1) << uint(i%64)
			}
			b.RetainUntypedParameter(node)
		}
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = node
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if ResolveFormat(visible, raw.Variadic, len(args), first.Usage) != Resolved {
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
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	// Retain this occurrence's unresolved type until every child has been
	// analyzed and the signature selected; a later occurrence may constrain it.
	if first.Usage.Type == nil {
		args[0], err = CoerceFormatFirst(b, first, b.Type(sqltype.Text).Usage())
		if err != nil {
			return nil, err
		}
	}
	if raw.Variadic && (args[1].ResultUsage().Type == nil || !args[1].ResultUsage().Type.BaseType().IsArray()) {
		return nil, expr.ErrorAt("42804", "VARIADIC argument must be an array", raw.Args[1].SourcePosition())
	}
	text := b.Type(sqltype.Text).Usage()
	if request, needed := FormatFirstCoercion(args[0].ResultUsage(), text); needed {
		first.Expression, first.Usage = args[0], args[0].ResultUsage()
		args[0], err = b.CoerceArgument(first, request)
		if err != nil {
			return nil, err
		}
	}
	properties := expr.IntrinsicProperties(FormatIntrinsicRule())
	for _, arg := range args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.NoteCatalogText()
		}
	}
	info, err := b.CollatedInfo(b.Info(text, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	formal, count := [2]sqltype.ID{sqltype.Text, sqltype.ID(2276)}, 1
	if raw.Variadic || len(args) > 1 {
		count = 2
	}
	info, err = b.BuiltinRoutineInfo(info, VariadicRoutineProvider, "format", formal[:count]...)
	if err != nil {
		return nil, err
	}
	int4 := b.Type(sqltype.Int4)
	b.Info(int4.Usage(), raw.Pos, expr.Properties{})
	return Format{Info: info, Args: args, Variadic: raw.Variadic, Int4: int4, RawUnknown: rawUnknown}, nil
}
