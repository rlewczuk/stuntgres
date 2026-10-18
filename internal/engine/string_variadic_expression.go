package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindStringVariadic(raw parser.FunctionExpr) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	concatenation, textInput := functions.StringVariadicRoles(name)
	var args []BoundExpr
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
	}
	var first functions.Argument
	for i, r := range raw.Args {
		operand, err := b.AnalyzeArgument(r, functions.StringVariadicAnalysis(name, i))
		if i == 0 && textInput {
			first = operand
		}
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = operand.Expression
		}
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	switch failure := functions.AnalyzeLabels(raw.ArgNames, raw.Args); failure.Kind {
	case functions.PositionalAfterNamed:
		return nil, expressionError("42601", "positional argument cannot follow named argument", failure.Pos)
	case functions.DuplicateLabel:
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	resolution := functions.ResolveStringVariadic(name, visible, raw.Variadic, raw.ResolutionOnly, len(raw.ArgNames), len(args), first.Usage)
	names := func() []string {
		result := make([]string, len(args))
		for i, arg := range args {
			result[i] = "unknown"
			literal, unknown := raw.Args[i].(parser.LiteralExpr)
			unknown = unknown && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue)
			if !unknown && arg != nil && arg.ResultUsage().Type != nil {
				result[i] = arg.ResultUsage().Type.DiagnosticName()
			}
			result[i] = functionNamedType(raw, i, result[i])
		}
		return result
	}
	if resolution != functions.Resolved {
		return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names())
	}
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if functions.StringVariadicCharAmbiguous(name, len(args), first.Usage) {
		d := expressionError("42725", fmt.Sprintf("function %s(%s) is not unique", objectNameText(raw.Name), strings.Join(names(), ", ")), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, d
	}
	text := b.typ(sqltype.Text).Usage()
	node := functions.StringVariadic{Name: name, Args: args, Variadic: raw.Variadic && concatenation, Stable: functions.StringVariadicIntrinsicStable(name), Frame: b.frameID()}
	if textInput && len(args) > 0 {
		args[0], err = functions.CoerceStringVariadicFirst(b, first, text)
		if err != nil {
			return nil, err
		}
		first.Expression, first.Usage = args[0], args[0].ResultUsage()
		selection := functions.ResolveStringVariadicFirst(name, first.Usage, text)
		node.AnyElement, node.Cast, node.CastPlanned, node.Stable = selection.AnyElement, selection.Cast, selection.CastPlanned, selection.Stable
		if selection.CoerceText {
			args[0], err = b.CoerceArgument(first, functions.StringVariadicFirstCoercion(selection))
			if err != nil {
				return nil, err
			}
		}
	}
	if node.Variadic {
		i := len(args) - 1
		if !functions.StringVariadicArray(args[i].ResultUsage()) {
			return nil, expressionError("42804", "VARIADIC argument must be an array", raw.Args[i].SourcePosition())
		}
	}
	properties := ExprProperties{Volatility: Immutable}
	if node.IntrinsicStable() {
		properties.Volatility = Stable
	}
	for _, arg := range args {
		if parameter, ok := arg.(parameterExpression); ok && parameter.Usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
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
	usage := text
	if id := functions.StringVariadicResult(name); id != usage.Type.ID() {
		usage = b.typ(id).Usage()
	}
	info, err := b.collatedInfo(b.info(usage, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	var formal [2]sqltype.ID
	count := 1
	switch name {
	case "pg_client_encoding":
		count = 0
	case "concat":
		formal[0] = sqltype.ID(2276)
	case "concat_ws":
		formal, count = [2]sqltype.ID{sqltype.Text, sqltype.ID(2276)}, 2
	default:
		formal[0] = sqltype.Text
		if node.AnyElement {
			formal[0] = sqltype.ID(2283)
		}
	}
	info, err = b.builtinRoutineInfo(info, variadicRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
	if err := b.capturePolymorphicBody(node); err != nil {
		return nil, err
	}
	return node, nil
}
