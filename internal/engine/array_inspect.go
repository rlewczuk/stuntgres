package engine

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindArrayInspect binds the array dimension inquiries. The shared call
// envelope precedes visibility; the analyzed operands are retained and no
// child is rebound.
func bindArrayInspect(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := functions.ArrayInspectFunction(name)
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
	if err := functions.CallDecorationError(raw); err != nil {
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
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: targets[i], Label: name})
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info := b.Info(b.Type(kind.Result()).Usage(), raw.Pos, properties)
	formal := [2]sqltype.ID{2277, sqltype.Int4}
	info, err = b.BuiltinRoutineInfo(info, functions.ArrayRoutineProvider, kind.Name(), formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	e := functions.ArrayInspect{Info: info, Kind: kind, Args: args, Namespace: arrayNamespace(raw.Name)}
	return e, e.Validate()
}

// analyzeArrayArguments analyzes ordinary arguments as comparison operands,
// then applies the shared FILTER/count/label envelope.
func analyzeArrayArguments(b functions.Binder, raw parser.FunctionExpr) ([]functions.Argument, error) {
	var small [3]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = arg
		}
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	return operands, nil
}

// arraySignatureError reports the missing signature with array-aware,
// labelled argument type names.
func arraySignatureError(b functions.Binder, raw parser.FunctionExpr, operands []functions.Argument) error {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = "unknown"
		if d := o.Usage.Type; d != nil {
			names[i] = functions.ArrayTypeName(b, d)
		}
		names[i] = functions.NamedArgumentType(raw, i, names[i])
	}
	return b.SignatureError(raw.Name, raw.Pos, names)
}

func arrayNamespace(name parser.ObjectName) string {
	if len(name.Parts) == 2 {
		return name.Parts[0].Text
	}
	return ""
}

func mapArrayInspectChildren(e functions.ArrayInspect, child func(BoundExpr) (BoundExpr, bool, error)) (functions.ArrayInspect, bool, error) {
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

func arrayInspectSQL(e functions.ArrayInspect, child func(BoundExpr) (string, error)) (string, error) {
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
