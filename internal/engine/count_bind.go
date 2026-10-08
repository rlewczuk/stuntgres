package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindCount(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	args := make([]BoundExpr, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.bind(child, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	if len(args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Name != "count" || (!raw.Star && len(args) > 1) {
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
		d := expressionError("42883", fmt.Sprintf("function %s(%s) does not exist", objectNameText(name), strings.Join(types, ", ")), raw.Pos).(*sqlstate.Error)
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		return nil, d
	}
	if !raw.Star && len(args) == 0 {
		return nil, expressionError("42809", objectNameText(name)+"(*) must be used to call a parameterless aggregate function", raw.Pos)
	}
	call := aggregateCall{provider: countProvider{b.typ(sqltype.Int8).Usage()}}
	properties := ExprProperties{StageDependent: true}
	if len(args) == 1 {
		if inner := aggregateOccurrence(args[0]); inner != nil {
			return nil, expressionError("42803", "aggregate function calls cannot be nested", inner.pos)
		}
		call.argument = args[0]
		p := args[0].Properties()
		properties.ParameterDependent, properties.Volatility = p.ParameterDependent, p.Volatility
		if parameter, ok := args[0].(parameterExpression); ok && parameter.usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
	}
	slot := len(b.aggregates)
	// Bind every occurrence before sharing; only immutable arguments may share.
	if call.argument == nil || call.argument.Properties().Volatility == Immutable {
		for i, existing := range b.aggregates {
			if b.sameGroupExpression(call.argument, existing.argument) {
				slot = i
				break
			}
		}
	}
	if slot == len(b.aggregates) {
		b.aggregates = append(b.aggregates, call)
	}
	return aggregateExpression{b.info(call.provider.ResultUsage(), raw.Pos, properties), slot}, nil
}
