package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindStringVariadic binds concat, concat_ws, quote_* and pg_client_encoding
// within the given query frame, which keys the quote capture cache.
func BindStringVariadic(b Binder, raw parser.FunctionExpr, frame uint32) (StringVariadic, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	concatenation, textInput := StringVariadicRoles(name)
	var args []expr.Expr
	if len(raw.Args) <= 100 {
		args = make([]expr.Expr, len(raw.Args))
	}
	var first Argument
	for i, r := range raw.Args {
		operand, err := b.AnalyzeArgument(r, StringVariadicAnalysis(name, i))
		if i == 0 && textInput {
			first = operand
		}
		if err != nil {
			return StringVariadic{}, err
		}
		if args != nil {
			args[i] = operand.Expression
		}
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return StringVariadic{}, fmt.Errorf("invalid function argument labels")
	}
	switch failure := AnalyzeLabels(raw.ArgNames, raw.Args); failure.Kind {
	case PositionalAfterNamed:
		return StringVariadic{}, expr.ErrorAt("42601", "positional argument cannot follow named argument", failure.Pos)
	case DuplicateLabel:
		return StringVariadic{}, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if len(raw.Args) > 100 {
		return StringVariadic{}, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return StringVariadic{}, err
	}
	resolution := ResolveStringVariadic(name, visible, raw.Variadic, raw.ResolutionOnly, len(raw.ArgNames), len(args), first.Usage)
	names := func() []string {
		result := make([]string, len(args))
		for i, arg := range args {
			result[i] = "unknown"
			literal, unknown := raw.Args[i].(parser.LiteralExpr)
			unknown = unknown && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue)
			if !unknown && arg != nil && arg.ResultUsage().Type != nil {
				result[i] = arg.ResultUsage().Type.DiagnosticName()
			}
			result[i] = NamedArgumentType(raw, i, result[i])
		}
		return result
	}
	if resolution != Resolved {
		return StringVariadic{}, ResolutionError(raw.Name, raw.Pos, names(), false)
	}
	switch {
	case raw.Star:
		return StringVariadic{}, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return StringVariadic{}, expr.ErrorAt("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return StringVariadic{}, expr.ErrorAt("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return StringVariadic{}, expr.ErrorAt("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if StringVariadicCharAmbiguous(name, len(args), first.Usage) {
		return StringVariadic{}, ResolutionError(raw.Name, raw.Pos, names(), true)
	}
	text := b.Type(sqltype.Text).Usage()
	node := StringVariadic{Name: name, Args: args, Variadic: raw.Variadic && concatenation, Stable: StringVariadicIntrinsicStable(name), Frame: frame}
	if textInput && len(args) > 0 {
		args[0], err = CoerceStringVariadicFirst(b, first, text)
		if err != nil {
			return StringVariadic{}, err
		}
		first.Expression, first.Usage = args[0], args[0].ResultUsage()
		selection := ResolveStringVariadicFirst(name, first.Usage, text)
		node.AnyElement, node.Cast, node.CastPlanned, node.Stable = selection.AnyElement, selection.Cast, selection.CastPlanned, selection.Stable
		if selection.CoerceText {
			args[0], err = b.CoerceArgument(first, StringVariadicFirstCoercion(selection))
			if err != nil {
				return StringVariadic{}, err
			}
		}
	}
	if node.Variadic {
		i := len(args) - 1
		if !StringVariadicArray(args[i].ResultUsage()) {
			return StringVariadic{}, expr.ErrorAt("42804", "VARIADIC argument must be an array", raw.Args[i].SourcePosition())
		}
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	if node.IntrinsicStable() {
		properties.Volatility = expr.Stable
	}
	for _, arg := range args {
		b.RetainUntypedParameter(arg)
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
	usage := text
	if id := StringVariadicResult(name); id != usage.Type.ID() {
		usage = b.Type(id).Usage()
	}
	info, err := b.CollatedInfo(b.Info(usage, raw.Pos, properties), args...)
	if err != nil {
		return StringVariadic{}, err
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
	info, err = b.BuiltinRoutineInfo(info, VariadicRoutineProvider, name, formal[:count]...)
	if err != nil {
		return StringVariadic{}, err
	}
	node.Info = info
	return node, nil
}
