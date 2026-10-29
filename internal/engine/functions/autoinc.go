package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func bindAutoinc(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) != 0 { names = make([]string,len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		name := "unknown"
		if arg.Usage.Type != nil { name = b.DiagnosticTypeName(arg.Usage.Type) }
		names[i] = NamedArgumentType(raw,i,name)
	}
	if err := ValidateCallSyntax(b,raw); err != nil { return nil,err }
	routine, err := b.InstalledRoutine(raw.Name, AutoincExtension, "")
	if err != nil { return nil,err }
	if routine == nil || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil,b.SignatureError(raw.Name,raw.Pos,names)
	}
	if err := routine.Validate(); err != nil { return nil,err }
	if raw.Star { return nil,expr.ErrorAt("42809","autoinc(*) specified, but autoinc is not an aggregate function",raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil,err }
	return routine.Attach(TriggerBuiltin{Info:b.Info(b.Type(2279).Usage(),raw.Pos,expr.Properties{Volatility:expr.Volatile}),Name:"autoinc"})
}
