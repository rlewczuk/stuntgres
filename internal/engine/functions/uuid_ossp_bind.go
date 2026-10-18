package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindUUIDOssp binds uuid-ossp's installed uuid_generate_v4. It sat before the
// engine's shared gates, so it owns labels and decorations. The engine
// resolves and owns the installed procedure; the call shares the v4 kernel.
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
	call, err := NewUUIDGeneration(b.Info(b.Type(sqltype.UUID).Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile}), "uuidv4", nil, nil)
	if err != nil {
		return nil, err
	}
	return routine.Attach(call)
}
