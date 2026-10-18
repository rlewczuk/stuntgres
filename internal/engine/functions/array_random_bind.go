package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// arrayRandomNames are the array sampling functions.
var arrayRandomNames = [...]string{"array_sample", "array_shuffle"}

// bindArrayRandom binds array_sample and array_shuffle after the shared call
// envelope; the result keeps the input array's collation.
func bindArrayRandom(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := ArrayRandomFunction(name)
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
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if targets[0].Type == nil {
		return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
	}
	args := make([]expr.Expr, len(operands))
	properties := expr.Properties{Volatility: expr.Volatile}
	for i, o := range operands {
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: targets[i], Label: name})
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
	e := ArrayRandom{Info: info, Kind: kind, Args: args, Namespace: arrayNamespace(raw.Name)}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	formal := [2]sqltype.ID{2277, sqltype.Int4}
	info, err = b.BuiltinRoutineInfo(info, ArrayRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}
