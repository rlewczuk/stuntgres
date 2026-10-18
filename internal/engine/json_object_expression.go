package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Explicit VARIADIC already carries its final array as one argument. These
// nonvariadic signatures therefore use exactly the ordinary argument path.
func (b *expressionBinder) bindTextObjectFunction(raw parser.FunctionExpr, name string, visible bool) (BoundExpr, error) {
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
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	definition, resolution := functions.ResolveTextObject(name, visible, len(raw.Args))
	if resolution != functions.Resolved {
		return missing()
	}
	target := b.typ(definition.Argument).Usage()
	if !functions.TextObjectArgumentsViable(operands, target) {
		return missing()
	}
	args, err := functions.CoerceTextObjectArguments(b, operands, target, definition.Intent)
	if err != nil {
		return nil, err
	}
	properties := ExprProperties{Volatility: Immutable}
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	usage := b.typ(definition.Result).Usage()
	formal := [2]sqltype.ID{definition.Argument, definition.Argument}
	info, err := b.builtinRoutineInfo(b.info(usage, raw.Pos, properties), jsonRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return functions.NewBinary(functionInfo(info), name, args, parser.OrdinaryFunctionSyntax)
}
