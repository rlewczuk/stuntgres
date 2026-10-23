package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ArgumentTypeName spells a bound argument for a signature diagnostic:
// string and NULL literals are unknown, as are untyped parameters.
func ArgumentTypeName(raw parser.Expr, arg expr.Expr) string {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return "unknown"
	}
	return DiagnosticNameOrUnknown(arg.ResultUsage().Type)
}

// ResolveCountSignature resolves the ordinary COUNT callable without
// registering aggregate state; window COUNT shares it.
func ResolveCountSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, args []expr.Expr) error {
	fact := AnalyzeCountSignature(raw.Name == "count", raw.Star, len(args))
	if fact == CountTooMany {
		return aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return err
	}
	if !visible || fact == CountMismatch {
		types := make([]string, len(args))
		for i, arg := range args {
			syntax, _ := OriginalArgument(b, raw.Args[i])
			types[i] = ArgumentTypeName(syntax, arg)
		}
		return UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	if fact == CountRequiresStar {
		return expr.ErrorAt("42809", qualifiedName(name)+"(*) must be used to call a parameterless aggregate function", raw.Pos)
	}
	return nil
}

// BindCount binds count(*) and count(any). The engine also binds every
// aggregate name it does not otherwise claim here, for the 42883 diagnostic.
func BindCount(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	args := make([]expr.Expr, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: BareProjectionArgument})
		if err != nil {
			return nil, err
		}
		args[i] = arg.Expression
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	if err := ResolveCountSignature(b, raw, name, args); err != nil {
		return nil, err
	}
	// The ANY formal leaves an untyped parameter argument pending unless
	// ORDER BY matching resolves it.
	call := AggregateCall{Provider: NewCountProvider(b.Type(sqltype.Int8).Usage()), Operation: AggregateCount, RetainUntypedArgument: true}
	if len(args) == 1 {
		call.Arguments = args
	}
	var formal [1]sqltype.ID
	count := 0
	if !raw.Star {
		formal[0], count = sqltype.ID(2276), 1
	}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, "count", formal[:count]...)
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, b.Info(call.Provider.ResultUsage(), raw.Pos, expr.Properties{}))
}

// windowCount resolves count with OVER: arguments and FILTER are analyzed,
// and window binding needs no coerced argument.
func windowCount(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	args := make([]expr.Expr, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: BareProjectionArgument})
		if err != nil {
			return nil, sqltype.Usage{}, err
		}
		args[i] = arg.Expression
	}
	if _, err := b.AnalyzeAggregateFilter(raw); err != nil {
		return nil, sqltype.Usage{}, err
	}
	return nil, b.Type(sqltype.Int8).Usage(), ResolveCountSignature(b, raw, name, args)
}
