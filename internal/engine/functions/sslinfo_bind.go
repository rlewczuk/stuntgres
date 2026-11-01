package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func bindSSLInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands []Argument
	if len(raw.Args) <= 100 { operands = make([]Argument, len(raw.Args)) }
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil { return nil, err }
		if operands != nil { operands[i] = o }
	}
	if len(raw.Args) > 100 { return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos) }
	if err := CallLabelError(raw); err != nil { return nil, err }
	names := installedArgumentNames(b, raw, operands)
	member, selected := SSLInfoLookup(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	routine, err := b.InstalledRoutine(raw.Name, SSLInfoExtension, "")
	if err != nil { return nil, err }
	if !selected || routine == nil || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.Star {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := routine.Validate(); err != nil { return nil, err }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	call := SSLInfo{Info: b.Info(b.Type(member.Result).Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile}), Name: member.Name}
	if err := call.Validate(); err != nil { return nil, err }
	return routine.Attach(call)
}
