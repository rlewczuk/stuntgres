package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// arrayInspectNames are the array dimension inquiries.
var arrayInspectNames = [...]string{"array_dims", "array_length", "array_lower", "array_upper", "array_ndims", "cardinality"}

// bindArrayInspect binds the array dimension inquiries. The shared call
// envelope precedes visibility; the analyzed operands are retained and no
// child is rebound.
func bindArrayInspect(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := ArrayInspectFunction(name)
	if !visible || kind == 0 || len(raw.ArgNames) != 0 || len(operands) != kind.Arity() {
		return signature()
	}
	var targets [2]sqltype.Usage
	catalogNull := false
	if d := operands[0].Usage.Type; d != nil {
		u := operands[0].Usage
		catalogNull = u.CatalogNullUsage()
		if catalogNull {
			targets[0] = u
		} else {
			base := d.BaseType()
			if !base.IsArray() {
				return signature()
			}
			targets[0] = base.Usage()
		}
	}
	targets[1] = b.Type(sqltype.Int4).Usage()
	for i, o := range operands {
		if o.Usage.Type != nil && !(i == 0 && catalogNull) {
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
	properties := expr.Properties{}
	for i, o := range operands {
		arg := o.Expression
		if !(i == 0 && catalogNull) {
			var err error
			arg, err = b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: targets[i], Label: name})
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info := b.Info(b.Type(kind.Result()).Usage(), raw.Pos, properties)
	formal := [2]sqltype.ID{2277, sqltype.Int4}
	info, err = b.BuiltinRoutineInfo(info, ArrayRoutineProvider, kind.Name(), formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	e := ArrayInspect{Info: info, Kind: kind, Args: args, Namespace: arrayNamespace(raw.Name)}
	return e, e.Validate()
}
