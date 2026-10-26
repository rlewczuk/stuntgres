package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)


// Scalar lookup cannot coerce an ordinary SQL argument, including unknown NULL,
// to INTERNAL. Aggregate invocation binds the real support through its private
// context recipe instead of manufacturing a scalar state datum.
func bindAggregateContextSupport(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	names := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		argument, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		names[i] = "unknown"
		if argument.Usage.Type != nil && argument.Usage.Type.Concrete() {
			names[i] = b.DiagnosticTypeName(argument.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if _, err := b.BuiltinVisible(raw.Name); err != nil {
		return nil, err
	}
	return nil, b.SignatureError(raw.Name, raw.Pos, names)
}
