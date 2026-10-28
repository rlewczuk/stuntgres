package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SettingRuntime resolves the live operation's effective settings, never a
// prepared statement's actor or a retained settings snapshot.
type SettingRuntime interface {
	InvokeSetting(string, []sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
}

type Setting struct {
	expr.Info
	Name string
	Args []expr.Expr
}

func bindSetting(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	valid := name == "current_setting" && (len(raw.Args) == 1 || len(raw.Args) == 2) || name == "set_config" && len(raw.Args) == 3 || name == "pg_settings_get_flags" && len(raw.Args) == 1
	if !valid || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.CallSignatureError(raw)
	}
	args, err := bindSettingArguments(b, name, raw)
	if err != nil {
		return nil, err
	}
	volatility := expr.Stable
	if name == "set_config" {
		volatility = expr.Volatile
	}
	properties := expr.Properties{Volatility: volatility}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	result := sqltype.Text
	if name == "pg_settings_get_flags" {
		result = 1009
	}
	info := b.Info(b.Type(result).Usage(), raw.Pos, properties)
	formals := []sqltype.ID{sqltype.Text}
	if name == "set_config" {
		formals = []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool}
	} else if len(args) == 2 {
		formals = append(formals, sqltype.Bool)
	}
	info, err = b.BuiltinRoutineInfo(info, SettingRoutineProvider, name, formals...)
	if err != nil {
		return nil, err
	}
	return Setting{Info: info, Name: name, Args: args}, nil
}

func (n Setting) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range n.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	if n.Name != "set_config" {
		for _, value := range values[:len(n.Args)] {
			if value.IsNull() {
				return sqltype.Null(n.Usage.Type), nil
			}
		}
	}
	runtime, ok := c.(SettingRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: setting call lacks statement authority")
	}
	return runtime.InvokeSetting(n.Name, values[:len(n.Args)], n.Usage.Type)
}

func (n Setting) Children() []expr.Expr { return n.Args }
func (n Setting) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("setting", children, len(n.Args)); err != nil {
		return nil, err
	}
	n.Info, n.Args = info, append([]expr.Expr(nil), children...)
	return n, nil
}
func (n Setting) IntrinsicVolatility() expr.Volatility {
	if n.Name == "set_config" {
		return expr.Volatile
	}
	return expr.Stable
}
func (Setting) PlanningFold() expr.Fold { return expr.FoldNever }
func (n Setting) StrictNull() expr.Strict {
	if n.Name != "set_config" {
		return expr.StrictAfterBare
	}
	return expr.StrictNone
}

func (Setting) ResultNullability(expr.ResultSite) (nullable, known bool) {
	return true, true
}
func (Setting) LooksLikeFunction() bool { return true }
func (n Setting) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(Setting)
	return ok && n.Name == o.Name && sameChildren(n.Args, o.Args, same)
}
func (n Setting) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{Tree: "Setting " + n.Name, Inputs: n.Args, Form: expr.FormCall, Name: n.Name, Quote: expr.QuoteIdentifier, Args: args, Replay: expr.Replay{RelationOnly: true, Validate: func() error {
		valid := n.Name == "current_setting" && (len(n.Args) == 1 || len(n.Args) == 2) || n.Name == "set_config" && len(n.Args) == 3 || n.Name == "pg_settings_get_flags" && len(n.Args) == 1
		result := sqltype.Text
		if n.Name == "pg_settings_get_flags" {
			result = 1009
		}
		if !valid || n.Usage.Type == nil || n.Usage.Type.ID() != result {
			return fmt.Errorf("stuntgres: invalid stored setting signature")
		}
		for i, arg := range n.Args {
			id := sqltype.Text
			if i == 2 || n.Name == "current_setting" && i == 1 {
				id = sqltype.Bool
			}
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
				return fmt.Errorf("stuntgres: invalid stored setting argument")
			}
		}
		return nil
	}}}, nil
}
func (n Setting) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(len(n.Args), len(n.Args))
}
func (n Setting) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Args, len(n.Args), written)
}
