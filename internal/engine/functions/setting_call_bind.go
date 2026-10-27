package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindSettingArguments checks builtin visibility and implicit call applicability
// before assigning current_setting/set_config operands to their fixed formals.
func bindSettingArguments(b Binder, function string, name parser.ObjectName, args []parser.Expr) ([]expr.Expr, error) {
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, b.CallSignatureError(parser.FunctionExpr{Name: name, Args: args, Pos: name.Parts[0].Pos})
	}
	formals := [3]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool}
	if function == "current_setting" {
		formals[1] = sqltype.Bool
	}
	var operands [3]Argument
	var targets [3]sqltype.Usage
	for i, raw := range args {
		operands[i], err = b.AnalyzeArgument(raw, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		typ := b.Type(formals[i])
		if typ == nil {
			return nil, fmt.Errorf("stuntgres: missing setting argument type")
		}
		targets[i] = typ.Usage()
	}
	if !knownCallEdges(b, operands[:len(args)], targets[:len(args)]) {
		return nil, b.CallSignatureError(parser.FunctionExpr{Name: name, Args: args, Pos: name.Parts[0].Pos})
	}
	bound := make([]expr.Expr, len(args))
	for i := range args {
		intent := DirectFormalArgument
		if operands[i].Usage.Type == nil {
			intent = AssignmentFormalArgument
		}
		bound[i], err = b.CoerceArgument(operands[i], CoercionRequest{Intent: intent, Target: targets[i]})
		if err != nil {
			return nil, err
		}
	}
	return bound, nil
}
