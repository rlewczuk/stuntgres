package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindUUIDOssp owns labels and decorations before the shared gates. Installed
// procedure authority remains with the engine, not the shared UUID kernel.
func bindUUIDOssp(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if names != nil {
			name := "unknown"
			if o.Usage.Type != nil {
				name = b.DiagnosticTypeName(o.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, UUIDOsspExtension, "")
	if err != nil {
		return nil, err
	}
	if len(raw.Args) != 0 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star || routine == nil {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	selector, volatility, _ := UUIDOsspLookup(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	call, err := NewUUIDGeneration(b.Info(b.Type(sqltype.UUID).Usage(), raw.Pos, expr.Properties{Volatility: volatility}), selector, nil, nil)
	if err != nil {
		return nil, err
	}
	return routine.Attach(call)
}
