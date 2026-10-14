package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func stringVariadicFunctionName(name string) bool {
	switch strings.ToLower(name) {
	case "concat", "concat_ws", "quote_ident", "quote_literal", "quote_nullable", "pg_client_encoding":
		return true
	}
	return false
}

// The signature remains separate from effective volatility: PostgreSQL's
// ANYELEMENT quote wrapper inlines a TEXT cast and an immutable quote call.
type stringVariadicExpression struct {
	expressionInfo
	name        string
	args        []BoundExpr
	variadic    bool
	anyelement  bool
	cast        sqltype.CoercionPlan
	castPlanned bool
	intrinsic   Volatility
	frame       uint32
	origin      quoteOrigin // execution-owned stored-view lowering; zero uses EvalContext
}

func (e stringVariadicExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var scratch [100]sqltype.Value
	if len(e.args) > len(scratch) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid string variadic arity")
	}
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		scratch[i] = v
	}
	output := c.Output
	output.Regclass, output.Regtype = c.liveScope(), c.liveScope()
	output.Regrole = c.liveScope()
	output.Regcollation, output.Regnamespace = c.liveScope(), c.liveScope()
	output.Regproc = c.liveScope()
	output.Operator = c.liveScope()
	if e.name == "pg_client_encoding" {
		return sqltype.String(e.usage.Type, output.ClientEncoding), nil
	}
	var out strings.Builder
	if e.name == "concat" || e.name == "concat_ws" {
		start, separator := 0, ""
		if e.name == "concat_ws" {
			if scratch[0].IsNull() {
				return sqltype.Null(e.usage.Type), nil
			}
			var err error
			separator, err = scratch[0].Text()
			if err != nil {
				return sqltype.Value{}, err
			}
			start = 1
		}
		values := formatValues{ordinary: scratch[start:len(e.args)]}
		if e.variadic {
			if scratch[start].IsNull() {
				return sqltype.Null(e.usage.Type), nil
			}
			values = formatValues{array: scratch[start]}
		}
		emitted := false
		for i := range values.len() {
			if err := c.Context.Err(); err != nil {
				return sqltype.Value{}, err
			}
			v := values.at(i)
			if v.IsNull() {
				continue
			}
			text, err := v.Type().OutputWithEnvironment(c.Context, v, output)
			if err != nil {
				return sqltype.Value{}, err
			}
			if emitted {
				if err := formatWrite(c.Context, &out, separator); err != nil {
					return sqltype.Value{}, err
				}
			}
			if err := formatWrite(c.Context, &out, text); err != nil {
				return sqltype.Value{}, err
			}
			emitted = true
		}
	} else {
		v := scratch[0]
		if v.IsNull() && e.name != "quote_nullable" {
			return sqltype.Null(e.usage.Type), nil
		}
		if e.anyelement && !v.IsNull() {
			if e.castPlanned {
				var err error
				v, err = e.cast.Apply(c.Context, v, c.Input, output)
				if err != nil {
					return sqltype.Value{}, err
				}
			} else {
				text, err := v.Type().OutputWithEnvironment(c.Context, v, output)
				if err != nil {
					return sqltype.Value{}, err
				}
				v = sqltype.String(e.usage.Type, text)
			}
		}
		verb := rune('L')
		if e.name == "quote_ident" {
			verb = 'I'
		}
		if err := formatField(c.Context, &out, v, verb, 0, false, output); err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(e.usage.Type, out.String()), nil
}

func (b *expressionBinder) bindStringVariadic(raw parser.FunctionExpr) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	concatenation := name == "concat" || name == "concat_ws"
	textInput := name == "quote_ident" || name == "quote_literal" || name == "quote_nullable" || name == "concat_ws"
	var args []BoundExpr
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
	}
	var first comparisonOperand
	for i, r := range raw.Args {
		var node BoundExpr
		var err error
		if i == 0 && textInput {
			first, err = b.stringOperand(r, true)
			node = first.node
		} else {
			node, err = b.bind(r, expressionPolicy{context: projectionContext, stringInput: true})
			if err == nil && unknownNullSyntax(r) {
				if literal, ok := node.(literalExpression); ok {
					literal.unknownNull = true
					node = literal
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = node
		}
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	named := false
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			if named {
				return nil, expressionError("42601", "positional argument cannot follow named argument", raw.Args[i].SourcePosition())
			}
			continue
		}
		named = true
		for _, previous := range raw.ArgNames[:i] {
			if previous.Text == label.Text {
				return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", label.Text), label.Pos)
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	valid := visible && len(raw.ArgNames) == 0 && name == strings.ToLower(name) && !raw.ResolutionOnly
	switch name {
	case "concat":
		valid = valid && len(args) >= 1 && (!raw.Variadic || len(args) == 1)
	case "concat_ws":
		valid = valid && len(args) >= 2 && (!raw.Variadic || len(args) == 2)
	case "pg_client_encoding":
		valid = valid && len(args) == 0
	default:
		valid = valid && len(args) == 1
	}
	if textInput && len(args) > 0 && first.descriptor() != nil && (name == "quote_ident" || name == "concat_ws") {
		valid = valid && sqltype.StringTextInput(first.descriptor().BaseType().ID())
	}
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
	if !valid {
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
	if !concatenation && name != "quote_ident" && len(args) == 1 && first.descriptor() != nil && first.descriptor().BaseType().ID() == sqltype.Char {
		d := expressionError("42725", fmt.Sprintf("function %s(%s) is not unique", objectNameText(raw.Name), strings.Join(names(), ", ")), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, d
	}
	text := b.typ(sqltype.Text).Usage()
	node := stringVariadicExpression{name: name, args: args, variadic: raw.Variadic && concatenation, intrinsic: Immutable, frame: b.frameID()}
	if concatenation || name == "pg_client_encoding" {
		node.intrinsic = Stable
	}
	if textInput && len(args) > 0 {
		if first.descriptor() == nil {
			args[0], err = b.finalizeOperand(first, text.Type)
			if err != nil {
				return nil, err
			}
		}
		source := args[0].ResultUsage()
		if !sqltype.StringTextInput(source.Type.BaseType().ID()) && name != "concat_ws" && name != "quote_ident" {
			node.anyelement = true
			// CIDR's selected SQL-wrapper TEXT conversion retains the observed mask.
			if source.Type.BaseType().ID() != sqltype.Cidr {
				node.cast, err = sqltype.PlanCoercion(source, text, sqltype.Explicit)
				node.castPlanned = err == nil
			}
			base := source.Type.BaseType()
			if base.IsEnum() || base.IsArray() {
				node.intrinsic = Stable
			} else if base.ID() != sqltype.Float4 && base.ID() != sqltype.Float8 && (node.castPlanned && node.cast.SessionDependent() || !node.castPlanned && base.OutputSessionDependent()) {
				node.intrinsic = Stable
			}
		} else if source.Type != text.Type {
			target := text
			if source.Collation.ID != 0 {
				target.Collation = source.Collation
			}
			var plan sqltype.CoercionPlan
			if source.Type.BaseType().ID() == sqltype.Char {
				plan, err = sqltype.PlanExtremaCharText(source, target)
			} else {
				plan, err = sqltype.PlanCoercion(source, target, sqltype.Implicit)
			}
			if err != nil {
				return nil, err
			}
			info, err := b.collatedInfo(b.info(target, args[0].SourcePosition(), coercionProperties(args[0], plan)), args[0])
			if err != nil {
				return nil, err
			}
			args[0] = coercionExpression{info, args[0], plan, sqltype.Implicit}
		}
	}
	if node.variadic {
		i := len(args) - 1
		if args[i].ResultUsage().Type == nil || !args[i].ResultUsage().Type.BaseType().IsArray() {
			return nil, expressionError("42804", "VARIADIC argument must be an array", raw.Args[i].SourcePosition())
		}
	}
	properties := ExprProperties{Volatility: node.intrinsic}
	for _, arg := range args {
		if parameter, ok := arg.(parameterExpression); ok && parameter.usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
	}
	usage := text
	if name == "pg_client_encoding" {
		usage = b.typ(sqltype.Name).Usage()
	}
	node.expressionInfo, err = b.collatedInfo(b.info(usage, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	return node, nil
}
