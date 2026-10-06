package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type nullCountKind uint8

const (
	countNulls nullCountKind = iota + 1
	countNonnulls
)

type nullCountExpression struct {
	expressionInfo
	kind nullCountKind
	args []BoundExpr
}

func (e nullCountExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.usage.Type == nil || e.usage.Type.ID() != sqltype.Int4 || len(e.args) == 0 || len(e.args) > 100 || (e.kind != countNulls && e.kind != countNonnulls) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid null-count expression")
	}
	var count int64
	for _, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		u := arg.ResultUsage()
		if u.Type == nil || !u.Type.Concrete() || value.Type() != u.Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid null-count argument type")
		}
		if value.IsNull() == (e.kind == countNulls) {
			count++
		}
	}
	return sqltype.Integer(e.usage.Type, count)
}

func (b *expressionBinder) bindFunction(raw parser.FunctionExpr) (BoundExpr, error) {
	if len(raw.Name.Parts) != 1 {
		return nil, fmt.Errorf("stuntgres: qualified function is unsupported")
	}
	name := raw.Name.Parts[0].Text
	var kind nullCountKind
	switch name {
	case "num_nulls":
		kind = countNulls
	case "num_nonnulls":
		kind = countNonnulls
	default:
		return nil, fmt.Errorf("stuntgres: function is unsupported")
	}
	// Over-limit calls still transform every child before resolution, but never
	// need retained executable argument storage.
	var args []BoundExpr
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
	}
	properties := ExprProperties{Volatility: Immutable}
	for i, rawArg := range raw.Args {
		// Casts remain selected arguments, but cannot hide computed arithmetic.
		// Former signed literal input remains admitted through either spelling.
		argument := rawArg
		for {
			cast, ok := argument.(parser.CastExpr)
			if !ok {
				break
			}
			argument = cast.Child
		}
		if operator, ok := argument.(parser.OperatorExpr); ok {
			if len(operator.Name.Parts) == 1 && operator.Name.Parts[0].Text == "+" && len(operator.Args) == 1 {
				if literal, ok := operator.Args[0].(parser.LiteralExpr); ok && (literal.Value.Kind == parser.IntegerValue || literal.Value.Kind == parser.NumericValue) {
					literal.Value.Pos = operator.Pos
					if _, wrapped := rawArg.(parser.CastExpr); !wrapped {
						rawArg = literal
					}
				} else {
					return nil, expressionError("0A000", "arithmetic in null-count arguments is unsupported", operator.Pos)
				}
			} else {
				return nil, expressionError("0A000", "arithmetic in null-count arguments is unsupported", operator.Pos)
			}
		}
		arg, err := b.bind(rawArg, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = arg
		}
		p := arg.Properties()
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		if parameter, ok := arg.(parameterExpression); ok && parameter.usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if len(args) == 0 || (raw.Variadic && len(args) != 1) {
		types := make([]string, len(args))
		for i, arg := range args {
			types[i] = "unknown"
			if literal, ok := raw.Args[i].(parser.LiteralExpr); ok && (literal.Value.Kind == parser.NullValue || literal.Value.Kind == parser.StringValue) {
				continue
			}
			if arg.ResultUsage().Type != nil {
				types[i] = arg.ResultUsage().Type.DiagnosticName()
			}
		}
		d := expressionError("42883", fmt.Sprintf("function %s(%s) does not exist", name, strings.Join(types, ", ")), raw.Pos).(*sqlstate.Error)
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		return nil, d
	}
	// Array expressions are not in the selected native scalar surface. Keep the
	// original operand location, including UNKNOWN string and NULL literals.
	if raw.Variadic {
		return nil, expressionError("42804", "VARIADIC argument must be an array", raw.Args[len(raw.Args)-1].SourcePosition())
	}
	return nullCountExpression{expressionInfo: b.info(b.typ(sqltype.Int4).Usage(), raw.Pos, properties), kind: kind, args: args}, nil
}
