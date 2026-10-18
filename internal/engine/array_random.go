package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionArrayRandomOwner() (expr.RandomOwner, error) {
	if c.liveScope() == nil || c.liveScope().op == nil || c.liveScope().op.session == nil {
		return nil, fmt.Errorf("stuntgres: missing array random session scope")
	}
	return &c.liveScope().op.session.arrayRandom, nil
}

// bindArrayRandom binds array_sample and array_shuffle after the shared call
// envelope; the result keeps the input array's collation.
func bindArrayRandom(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands, err := analyzeArrayArguments(b, raw)
	if err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	signature := func() (expr.Expr, error) { return nil, arraySignatureError(b, raw, operands) }
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := functions.ArrayRandomFunction(name)
	if !visible || kind == 0 || len(raw.ArgNames) != 0 || len(operands) != kind.Arity() {
		return signature()
	}
	var targets [2]sqltype.Usage
	if d := operands[0].Usage.Type; d != nil {
		base := d.BaseType()
		if !base.IsArray() {
			return signature()
		}
		targets[0] = base.Usage()
		targets[0].Collation = operands[0].Usage.Collation
	}
	targets[1] = b.Type(sqltype.Int4).Usage()
	for i, o := range operands {
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, targets[i], sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if targets[0].Type == nil {
		return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
	}
	args := make([]expr.Expr, len(operands))
	properties := expr.Properties{Volatility: expr.Volatile}
	for i, o := range operands {
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: targets[i], Label: name})
		if err != nil {
			return nil, err
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info := b.Info(targets[0], raw.Pos, properties)
	if targets[0].Collation.ID != 0 {
		info, err = b.CollatedInfo(info, args...)
		if err != nil {
			return nil, err
		}
	}
	e := functions.ArrayRandom{Info: info, Kind: kind, Args: args, Namespace: arrayNamespace(raw.Name)}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	formal := [2]sqltype.ID{2277, sqltype.Int4}
	info, err = b.BuiltinRoutineInfo(info, functions.ArrayRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}

func mapArrayRandomChildren(e functions.ArrayRandom, child func(BoundExpr) (BoundExpr, bool, error)) (functions.ArrayRandom, bool, error) {
	var args []BoundExpr
	for i, old := range e.Args {
		next, changed, err := child(old)
		if err != nil {
			return e, false, err
		}
		if changed {
			if args == nil {
				args = append([]BoundExpr(nil), e.Args...)
			}
			args[i] = next
		}
	}
	if args == nil {
		return e, false, nil
	}
	e.Args = args
	return e, true, nil
}
func arrayRandomSQL(e functions.ArrayRandom, child func(BoundExpr) (string, error)) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	return "pg_catalog." + e.Kind.Name() + "(" + strings.Join(parts, ", ") + ")", nil
}
