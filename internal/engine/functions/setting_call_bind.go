package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindSettingCall binds the arguments of current_setting(text) or
// set_config(text, text, bool): builtin visibility first, then each argument
// in assignment context to its fixed formal, then the routine identity.
func BindSettingCall(b Binder, function string, name parser.ObjectName, args []parser.Expr) ([]expr.Expr, ids.ObjectRef, error) {
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	if !visible {
		return nil, ids.ObjectRef{}, b.CallSignatureError(parser.FunctionExpr{Name: name, Args: args, Pos: name.Parts[0].Pos})
	}
	formals := [3]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool}
	bound := make([]expr.Expr, len(args))
	for i, raw := range args {
		typ := b.Type(formals[i])
		if typ == nil {
			return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: missing setting argument type")
		}
		bound[i], err = b.CoerceArgument(Argument{Raw: raw}, CoercionRequest{Intent: AssignmentFormalArgument, Target: typ.Usage()})
		if err != nil {
			return nil, ids.ObjectRef{}, err
		}
	}
	routine, err := b.BuiltinRoutineReference(SettingRoutineProvider, function, formals[:len(bound)]...)
	return bound, routine, err
}
