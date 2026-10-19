package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindFunctionProvider(raw parser.FunctionExpr) (expr.Expr, error) {
	if len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid function name")
	}
	if node, handled, err := b.bindSQLCallable(raw); handled {
		return node, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	// Registered families are name-disjoint from the chain below.
	if definition, ok := functions.Lookup(name); ok {
		if definition.SharedGates {
			if err := b.sharedCallGates(raw, definition); err != nil {
				return nil, err
			}
		}
		return definition.Bind(b, raw)
	}
	if parser.CatalogRelationFunctionName(name) {
		return b.bindExtensionUpdatePaths(raw)
	}
	if raw.Syntax == parser.SimilarEscapeFunctionSyntax {
		return functions.BindSimilarEscape(b, raw)
	}
	if parser.VersionInformationFunctionName(name) {
		return b.bindVersionInformation(raw)
	}
	if strings.EqualFold(name, "generate_series") {
		visible, err := b.builtinVisible(raw.Name, functionLookup)
		if err != nil {
			return nil, err
		}
		return b.bindSeries(raw, visible)
	}
	if strings.EqualFold(name, "generate_subscripts") {
		visible, err := b.builtinVisible(raw.Name, functionLookup)
		if err != nil {
			return nil, err
		}
		return b.bindSubscripts(raw, visible)
	}
	if strings.EqualFold(name, "unnest") {
		return b.bindUnnest(raw)
	}
	if parser.NetworkFormatFunctionName(name) {
		return b.bindNetworkFormat(raw)
	}
	if parser.BlockingInquiryFunctionName(name) {
		return b.bindBlockingInquiry(raw)
	}
	// Shared syntax must never silently discard labels in adjacent providers.
	if len(raw.ArgNames) != 0 && !parser.AdvisoryFunctionName(name) {
		return nil, b.functionSignatureError(raw)
	}
	if raw.ResolutionOnly {
		return nil, b.formatDiagnosticSignatureError(raw)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	var kind functions.NullCountKind
	switch name {
	case "enum_first", "enum_last", "enum_range":
		return b.bindEnumFunction(raw, visible)
	case "overlaps":
		node, err := functions.BindDatetimeOverlaps(b, raw, visible)
		if err != nil {
			return nil, err
		}
		// Inline safety walks engine nodes; the node protocol replaces it (F2).
		e := node.(functions.DatetimeOverlaps)
		e.WrapperContext = !overlapsInlineSafe(e)
		return e, nil
	case "pg_size_bytes", "pg_size_pretty":
		return b.bindSize(raw, visible)
	case "pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared", "pg_advisory_unlock_all", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_xact_lock", "pg_advisory_xact_lock_shared", "pg_try_advisory_xact_lock", "pg_try_advisory_xact_lock_shared":
		return b.bindAdvisory(raw, visible)
	default:
		kind = functions.NullCountFunctionKind(name)
		if kind != 0 {
			break
		}
		return nil, b.unavailableFunctionError(raw, visible)
	}
	// Over-limit calls still transform every child before resolution, but never
	// need retained executable argument storage.
	var args []expr.Expr
	if len(raw.Args) <= 100 {
		args = make([]expr.Expr, len(raw.Args))
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, rawArg := range raw.Args {
		analyzed, issue, err := functions.AnalyzeNullCountArgument(b, rawArg)
		if err != nil {
			return nil, err
		}
		if issue.UnsupportedArithmetic {
			return nil, expr.ErrorAt("0A000", "arithmetic in null-count arguments is unsupported", issue.Position)
		}
		rawArg, arg := analyzed.Raw, analyzed.Expression
		if unknownNullSyntax(rawArg) {
			literal, ok := arg.(literalExpression)
			if !ok || !literal.value.IsNull() || literal.Usage.Type.ID() != sqltype.Text {
				return nil, fmt.Errorf("stuntgres: invalid unknown null-count argument")
			}
			literal.UnknownNull = true
			arg = literal
		}
		if args != nil {
			args[i] = arg
		}
		p := arg.Properties()
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if p.Volatility > properties.Volatility {
			properties.Volatility = p.Volatility
		}
		if parameter, ok := arg.(parameterExpression); ok && parameter.Usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if !functions.NullCountAllowed(visible, raw.Variadic, len(args)) {
		types := make([]string, len(args))
		for i, arg := range args {
			types[i] = "unknown"
			syntax, _ := b.OriginalArgument(raw.Args[i])
			if literal, ok := syntax.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.NullValue || literal.Value.Kind == parser.StringValue) {
				continue
			}
			if arg.ResultUsage().Type != nil {
				types[i] = arg.ResultUsage().Type.DiagnosticName()
			}
		}
		d := expr.ErrorAt("42883", fmt.Sprintf("function %s(%s) does not exist", objectNameText(raw.Name), strings.Join(types, ", ")), raw.Pos).(*sqlstate.Error)
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		return nil, d
	}
	// Ordinary arguments count whole-value nullness, including arrays. Explicit
	// VARIADIC array expansion remains outside this provider's capability.
	if raw.Variadic {
		if functions.NullCountVariadicArray(args[len(args)-1].ResultUsage()) {
			return nil, expr.ErrorAt("0A000", "VARIADIC arrays in null-count arguments are unsupported", raw.Args[len(raw.Args)-1].SourcePosition())
		}
		return nil, expr.ErrorAt("42804", "VARIADIC argument must be an array", raw.Args[len(raw.Args)-1].SourcePosition())
	}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.Int4).Usage(), raw.Pos, properties), variadicRoutineProvider, name, sqltype.ID(2276))
	if err != nil {
		return nil, err
	}
	return functions.NullCount{Info: info, Kind: kind, Args: args}, nil
}

// sharedCallGates applies, for a registered family, the label and
// ResolutionOnly gates that the name chain applies before its final switch.
// Other spellings resolve only unless the family owns that diagnostic boundary.
func (b *expressionBinder) sharedCallGates(raw parser.FunctionExpr, definition functions.Definition) error {
	if len(raw.ArgNames) != 0 && !definition.OwnsLabels {
		return b.functionSignatureError(raw)
	}
	if raw.ResolutionOnly || !definition.OwnsSpelling && !slices.Contains(definition.Names, raw.Name.Parts[len(raw.Name.Parts)-1].Text) {
		return b.formatDiagnosticSignatureError(raw)
	}
	return nil
}

// The admitted format failure paths follow argument transformation, global
// syntax limit, namespace resolution, then missing-signature diagnostics.
func (b *expressionBinder) formatDiagnosticSignatureError(raw parser.FunctionExpr) error {
	if len(raw.Args) > 100 {
		for _, arg := range raw.Args {
			if _, err := b.bind(arg, expressionPolicy{context: projectionContext}); err != nil {
				return err
			}
		}
		return expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	names, _, err := b.functionArgumentTypeNames(raw)
	if err != nil {
		return err
	}
	if _, err := b.builtinVisible(raw.Name, functionLookup); err != nil {
		return err
	}
	return b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
}

// functionSignatureError binds diagnostic operands once, only on a failure path.
func (b *expressionBinder) functionSignatureError(raw parser.FunctionExpr) error {
	// Syntactic admission preserves quoted spelling. On this failure route
	// only, the argument limit still precedes exact-name signature resolution.
	if len(raw.Args) > 100 && len(raw.Name.Parts) > 0 {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		selectedName := parser.AdvisoryFunctionName(name)
		if !selectedName {
			for _, selected := range [...]string{"sum", "convert", "convert_from", "convert_to", "encode", "decode", "to_bin", "to_oct", "to_hex", "crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512", "pg_size_bytes", "pg_size_pretty", "bit_count", "get_bit", "get_byte", "set_bit", "set_byte"} {
				if len(name) == len(selected) && strings.EqualFold(name, selected) {
					selectedName = true
					break
				}
			}
		}
		if selectedName {
			for _, rawArg := range raw.Args {
				if _, err := b.bind(rawArg, expressionPolicy{context: projectionContext}); err != nil {
					return err
				}
			}
			return expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
		}
	}
	names, _, err := b.functionArgumentTypeNames(raw)
	if err != nil {
		return err
	}
	return b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
}

// Diagnostic operands are transformed before selected identity namespace lookup.
func (b *expressionBinder) functionArgumentTypeNames(raw parser.FunctionExpr) ([]string, bool, error) {
	names := make([]string, len(raw.Args))
	composite := false
	for i, rawArg := range raw.Args {
		arg, err := b.bind(rawArg, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, false, err
		}
		names[i] = functionArgumentTypeName(rawArg, arg)
		if typ := arg.ResultUsage().Type; typ != nil && names[i] != "unknown" {
			names[i] = b.diagnosticTypeName(typ)
			composite = composite || typ.BaseType().IsComposite()
		}
		names[i] = functions.NamedArgumentType(raw, i, names[i])
	}
	return names, composite, nil
}

func functionArgumentTypeName(raw parser.Expr, arg expr.Expr) string {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return "unknown"
	}
	if arg.ResultUsage().Type != nil {
		return arg.ResultUsage().Type.DiagnosticName()
	}
	return "unknown"
}

// Reuse transformed argument names when a diagnostic producer already bound them.
func functionSignatureErrorForTypes(name parser.ObjectName, pos parser.Position, names []string) error {
	return functionResolutionErrorForTypes(name, pos, names, false)
}

func (b *expressionBinder) functionSignatureErrorForTypes(name parser.ObjectName, pos parser.Position, names []string) error {
	return b.functionResolutionErrorForTypes(name, pos, names, false)
}

func (b *expressionBinder) functionResolutionErrorForTypes(name parser.ObjectName, pos parser.Position, names []string, ambiguous bool) error {
	err := functionResolutionErrorForTypes(name, pos, names, ambiguous)
	if !ambiguous {
		b.noteFunctionSignature(name, pos, err)
	}
	return err
}

// Temporary alias: the pure builder moved to functions (F4/F5 math).
var functionResolutionErrorForTypes = functions.ResolutionError

// unknownNullSyntax recognizes provenance carriers, not a concrete UNKNOWN type.
// Callers still resolve any annotation through the ordinary catalog/type path.
func unknownNullSyntax(raw parser.Expr) bool {
	if cast, ok := raw.(parser.CastExpr); ok {
		if cast.Syntax == parser.FunctionCast || cast.TypeName.Name != "unknown" {
			return false
		}
		raw = cast.Child
	}
	literal, ok := raw.(parser.LiteralExpr)
	return ok && literal.Value.Kind == parser.NullValue
}
