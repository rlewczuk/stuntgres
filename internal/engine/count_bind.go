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
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
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
	call := aggregateCall{provider: countProvider{b.typ(sqltype.Int8).Usage()}, operation: aggregateCount}
	if len(args) == 1 {
		call.argument = args[0]
	}
	result, err := b.finishAggregate(raw, call, filter, b.info(call.provider.ResultUsage(), raw.Pos, ExprProperties{}))
	if err != nil {
		return nil, err
	}
	// Modifiers can resolve a matched UNKNOWN input target. Only an actual
	// remaining ANY occurrence participates in the unresolved-parameter guard.
	argument := b.aggregates[result.(aggregateExpression).slot].argument
	if parameter, ok := argument.(parameterExpression); ok && parameter.usage.Type == nil {
		b.anyParameters = append(b.anyParameters, parameter)
	}
	return result, nil
}
