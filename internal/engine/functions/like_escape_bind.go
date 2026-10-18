package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// bindLikeEscape runs after the shared call gates; builtin visibility follows
// them, as it did in the engine's final name switch.
func bindLikeEscape(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return bindLikeEscapeVisible(b, raw, visible)
}

func bindLikeEscapeVisible(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	if len(raw.Args) != 2 {
		return nil, b.CallSignatureError(raw)
	}
	var operands [2]Argument
	var names []string
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = o
		name := "unknown"
		if o.Usage.Type != nil {
			name = o.Usage.Type.DiagnosticName()
		}
		names = append(names, name)
	}
	missing := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	targetID := LikeEscapeTarget(operands)
	if !LikeEscapeAllowed(visible, raw.Variadic, operands, targetID) {
		return missing()
	}
	target := b.Type(targetID)
	pattern, escape, err := CoerceLikeEscapeArguments(b, operands, target.Usage())
	if err != nil {
		return nil, err
	}
	u := target.Usage()
	info, err := b.CollatedInfo(b.Info(u, raw.Pos, expr.MergeProperties(pattern.Properties(), escape.Properties())), pattern, escape)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinRoutineInfo(info, StringRoutineProvider, "like_escape", targetID, targetID)
	if err != nil {
		return nil, err
	}
	return NewLikeEscape(info, pattern, escape)
}
