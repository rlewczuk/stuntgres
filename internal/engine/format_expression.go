package engine

import (
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// format retains ANY operands in their original types: OUTPUT is not the
// implicit TEXT conversion used by strict string functions or concatenation.
type formatExpression struct {
	expressionInfo
	args     []BoundExpr
	variadic bool
	int4     *sqltype.Descriptor
}

func (e formatExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if len(e.args) < 1 || len(e.args) > 100 || e.usage.Type == nil || e.usage.Type.ID() != sqltype.Text || e.int4 == nil || e.int4.ID() != sqltype.Int4 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid format expression")
	}
	var scratch [100]sqltype.Value
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != arg.ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid format argument type")
		}
		scratch[i] = value
	}
	// NONSTRICT does not suppress evaluation of NULL-format or unused children.
	if scratch[0].IsNull() {
		return sqltype.Null(e.usage.Type), nil
	}
	pattern, err := scratch[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	values := formatValues{ordinary: scratch[1:len(e.args)]}
	if e.variadic {
		if len(e.args) != 2 {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid variadic format arity")
		}
		values = formatValues{array: scratch[1]}
	}
	output := c.Output
	output.Regclass = c.statementScope
	output.Regtype = c.statementScope
	text, err := formatText(c.Context, pattern, values, e.int4, output)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(e.usage.Type, text), nil
}

func (b *expressionBinder) bindFormat(raw parser.FunctionExpr) (BoundExpr, error) {
	var args []BoundExpr
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
	}
	var first comparisonOperand
	for i, r := range raw.Args {
		var node BoundExpr
		var err error
		if i == 0 {
			first, err = b.stringOperand(r, true)
			if err == nil {
				node = first.node
			}
		} else {
			node, err = b.bind(r, expressionPolicy{context: projectionContext})
			if err == nil {
				if unknownNullSyntax(r) {
					if literal, ok := node.(literalExpression); ok {
						literal.unknownNull = true
						node = literal
					}
				}
				if parameter, ok := node.(parameterExpression); ok && parameter.usage.Type == nil {
					b.anyParameters = append(b.anyParameters, parameter)
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
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	valid := visible && len(args) > 0 && (!raw.Variadic || len(args) == 2)
	if len(args) > 0 && first.descriptor() != nil {
		valid = valid && sqltype.StringTextInput(first.descriptor().BaseType().ID())
	}
	if !valid {
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
		return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	// Retain this occurrence's unresolved type until every child has been
	// analyzed and the signature selected; a later occurrence may constrain it.
	if first.descriptor() == nil {
		args[0], err = b.finalizeOperand(first, b.typ(sqltype.Text))
		if err != nil {
			return nil, err
		}
	}
	if raw.Variadic && (args[1].ResultUsage().Type == nil || !args[1].ResultUsage().Type.BaseType().IsArray()) {
		return nil, expressionError("42804", "VARIADIC argument must be an array", raw.Args[1].SourcePosition())
	}
	text := b.typ(sqltype.Text).Usage()
	if args[0].ResultUsage().Type != text.Type {
		source := args[0].ResultUsage()
		target := text
		if source.Collation.ID != 0 {
			target.Collation = source.Collation
		}
		var plan sqltype.CoercionPlan
		var err error
		if source.Type.BaseType().ID() == sqltype.Char {
			plan, err = sqltype.PlanExtremaCharText(source, target)
		} else {
			plan, err = sqltype.PlanCoercion(source, target, sqltype.Implicit)
		}
		if err != nil {
			return nil, err
		}
		info, err := collatedInfo(b.info(target, args[0].SourcePosition(), coercionProperties(args[0], plan)), args[0])
		if err != nil {
			return nil, err
		}
		args[0] = coercionExpression{info, args[0], plan, sqltype.Implicit}
	}
	properties := ExprProperties{Volatility: Stable}
	for _, arg := range args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
	}
	info, err := collatedInfo(b.info(text, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	int4 := b.typ(sqltype.Int4)
	b.info(int4.Usage(), raw.Pos, ExprProperties{})
	return formatExpression{expressionInfo: info, args: args, variadic: raw.Variadic, int4: int4}, nil
}
