package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Explicit VARIADIC already carries its final array as one argument. These
// nonvariadic signatures therefore use exactly the ordinary argument path.
func bindTextObjectFunction(b functions.Binder, raw parser.FunctionExpr, name string, visible bool) (expr.Expr, error) {
	var small [2]functions.Argument
	var smallNames [2]string
	var operands []functions.Argument
	var names []string
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
		names = smallNames[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, functions.AnalysisRequest{Intent: functions.StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if names != nil {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	definition, resolution := functions.ResolveTextObject(name, visible, len(raw.Args))
	if resolution != functions.Resolved {
		return missing()
	}
	target := b.Type(definition.Argument).Usage()
	if !functions.TextObjectArgumentsViable(operands, target) {
		return missing()
	}
	args, err := functions.CoerceTextObjectArguments(b, operands, target, definition.Intent)
	if err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	usage := b.Type(definition.Result).Usage()
	formal := [2]sqltype.ID{definition.Argument, definition.Argument}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), functions.JSONRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return functions.NewBinary(info, name, args, parser.OrdinaryFunctionSyntax)
}
