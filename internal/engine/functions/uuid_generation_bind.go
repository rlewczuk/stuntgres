package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindUUIDGeneration binds uuidv4 and uuidv7 after the engine's shared call
// gates (SharedGates; uuidv7 owns its shift label). Builtin visibility
// follows the gates, as it did in the engine's final name switch.
func bindUUIDGeneration(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return BindUUIDGeneration(b, raw, visible)
}

// BindUUIDGeneration binds a UUID generator call once visibility is known.
// pgcrypto's gen_random_uuid also binds here when pg_catalog wins the path.
func BindUUIDGeneration(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, unknown, err := AnalyzeDirectArguments(b, raw.Args)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (expr.Expr, error) {
		types := make([]string, len(args))
		for i, arg := range args {
			typ := "unknown"
			if !unknown[i] && arg.ResultUsage().Type != nil {
				typ = arg.ResultUsage().Type.DiagnosticName()
			}
			types[i] = NamedArgumentType(raw, i, typ)
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, types)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	if !UUIDGenerationShape(raw, visible) {
		return signature()
	}
	properties := expr.Properties{Volatility: expr.Volatile}
	var timestamp *sqltype.Descriptor
	if len(args) != 0 {
		target := b.Type(sqltype.Interval).Usage()
		argument := Argument{Expression: args[0], Raw: raw.Args[0]}
		if !unknown[0] {
			argument.Usage = args[0].ResultUsage()
		}
		if !UUIDShiftCompatible(argument.Usage, target) {
			return signature()
		}
		args[0], err = CoerceUUIDShift(b, argument, target)
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[0].Properties())
		timestamp = b.Type(sqltype.Timestamptz)
	}
	node, err := NewUUIDGeneration(expr.Info{}, name, args, timestamp)
	if err != nil {
		return nil, err
	}
	var formal [1]sqltype.ID
	if len(args) != 0 {
		formal[0] = sqltype.Interval
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.UUID).Usage(), raw.Pos, properties), UUIDRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
