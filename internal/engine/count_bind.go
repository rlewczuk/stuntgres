package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Resolve the ordinary COUNT callable without registering aggregate state.
func (b *expressionBinder) resolveCountSignature(raw parser.AggregateExpr, name parser.ObjectName, args []BoundExpr) error {
	fact := functions.AnalyzeCountSignature(raw.Name == "count", raw.Star, len(args))
	if fact == functions.CountTooMany {
		return expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return err
	}
	if !visible || fact == functions.CountMismatch {
		types := make([]string, len(args))
		for i, arg := range args {
			types[i] = functionArgumentTypeName(raw.Args[i], arg)
		}
		return functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	if fact == functions.CountRequiresStar {
		return expressionError("42809", objectNameText(name)+"(*) must be used to call a parameterless aggregate function", raw.Pos)
	}
	return nil
}

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
	if err := b.resolveCountSignature(raw, name, args); err != nil {
		return nil, err
	}
	call := aggregateCall{provider: functions.NewCountProvider(b.typ(sqltype.Int8).Usage()), operation: aggregateCount}
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
