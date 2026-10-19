package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// arrayTransformNames are the array transform functions.
var arrayTransformNames = [...]string{"array_append", "array_prepend", "array_cat", "array_fill", "array_reverse", "array_sort", "trim_array", "array_to_string"}

// bindArrayTransform binds the array transform functions. Named arguments
// are admitted only for array_sort's three formals.
func bindArrayTransform(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands, err := analyzeArrayArguments(b, raw)
	if err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	signature := func() (expr.Expr, error) { return nil, arraySignatureError(b, raw, operands) }
	if raw.Within {
		// Ordered-set lookup includes the ORDER BY inputs in the signature.
		// No selected transform has an ordered-set overload.
		for _, key := range raw.Order {
			o, err := b.AnalyzeArgument(key.Expr, AnalysisRequest{Intent: ComparisonArgument})
			if err != nil {
				return nil, err
			}
			operands = append(operands, o)
		}
		return signature()
	}
	kind := ArrayTransformFunction(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !visible || !kind.Arity(len(operands)) {
		return signature()
	}
	slots := [3]uint8{0, 1, 2}
	labels := [3]string{}
	if len(raw.ArgNames) != 0 {
		if kind != ArraySort || len(operands) == 1 {
			return signature()
		}
		var seen [3]bool
		for i, label := range raw.ArgNames {
			slot := i
			if label.Text != "" {
				slot = -1
				for j, name := range [3]string{"array", "descending", "nulls_first"} {
					if name == label.Text {
						slot = j
						break
					}
				}
				labels[i] = label.Text
			}
			if slot < 0 || slot >= len(operands) || seen[slot] {
				return signature()
			}
			slots[i] = uint8(slot)
			seen[slot] = true
		}
	}
	return FinishArrayTransform(b, raw, kind, operands, slots, labels, false, signature)
}

// FinishArrayTransform selects and coerces a transform's formals. The ||
// operator reaches it in operator mode with already analyzed operands.
func FinishArrayTransform(b Binder, raw parser.FunctionExpr, kind ArrayTransformKind, operands []Argument, slots [3]uint8, labels [3]string, operator bool, signature func() (expr.Expr, error)) (expr.Expr, error) {
	var formal [3]Argument
	for i, o := range operands {
		formal[slots[i]] = o
	}
	var targets [3]sqltype.Usage
	var result sqltype.Usage
	compatible := kind == ArrayAppend || kind == ArrayPrepend || kind == ArrayConcatenate
	if compatible {
		arrays := [2]bool{kind != ArrayPrepend, kind != ArrayAppend}
		var ok bool
		result, ok = compatibleArrayTargets(b, formal[:len(operands)], arrays[:], targets[:len(operands)], true)
		if !ok {
			return signature()
		}
	} else {
		u := formal[0].Usage
		if kind == ArrayFill {
			if u.Type != nil {
				array, ok := b.ArrayType(u.Type)
				if !ok {
					return nil, expr.ErrorAt("42704", "could not find array type for data type "+ArrayTypeName(b, u.Type), parser.Position{})
				}
				if !array.Concrete() {
					return signature()
				}
				result = array.Usage()
				targets[0] = u
			}
			integer, ok := b.ArrayType(b.Type(sqltype.Int4))
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing integer array association")
			}
			targets[1], targets[2] = integer.Usage(), integer.Usage()
		} else {
			var ok bool
			targets[0], ok = arrayInputTarget(u, true)
			if !ok {
				return signature()
			}
			result = targets[0]
			switch kind {
			case ArraySort:
				targets[1], targets[2] = b.Type(sqltype.Bool).Usage(), b.Type(sqltype.Bool).Usage()
			case ArrayTrim:
				targets[1] = b.Type(sqltype.Int4).Usage()
			case ArrayToString:
				result = b.Type(sqltype.Text).Usage()
				targets[1], targets[2] = result, result
			}
		}
	}
	// Probe every known edge before importing any unknown literal or parameter.
	if !knownCallEdges(formal[:len(operands)], targets[:len(operands)]) {
		return signature()
	}
	name := kind.Name()
	if !operator {
		if raw.Star {
			return nil, expr.ErrorAt("42809", name+"(*) specified, but "+qualifiedName(raw.Name)+" is not an aggregate function", raw.Pos)
		}
		if err := CallDecorationError(raw); err != nil {
			return nil, err
		}
	}
	if targets[0].Type == nil {
		return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
	}
	args := make([]expr.Expr, len(operands))
	properties := expr.Properties{}
	for i, o := range operands {
		target := targets[slots[i]]
		arg := o.Expression
		if !target.CatalogNullUsage() {
			var err error
			arg, err = b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if kind == ArrayToString {
		properties.Volatility = max(properties.Volatility, expr.Stable)
		if targets[0].Type.ContainsCatalogIdentifier() {
			b.NoteCatalogText()
		}
	}
	info := b.Info(result, raw.Pos, properties)
	if result.Collation.ID != 0 {
		var err error
		info, err = b.CollatedInfo(info, args...)
		if err != nil {
			return nil, err
		}
	}
	// Capture the selected released formal family, not the concrete coerced
	// array/element types or the written order of named sort arguments.
	var inputs [3]sqltype.ID
	switch kind {
	case ArrayAppend:
		inputs[0], inputs[1] = 5078, 5077
	case ArrayPrepend:
		inputs[0], inputs[1] = 5077, 5078
	case ArrayConcatenate:
		inputs[0], inputs[1] = 5078, 5078
	case ArrayFill:
		inputs[0], inputs[1], inputs[2] = 2283, 1007, 1007
	case ArrayReverse:
		inputs[0] = 2277
	case ArraySort:
		inputs[0], inputs[1], inputs[2] = 2277, sqltype.Bool, sqltype.Bool
	case ArrayTrim:
		inputs[0], inputs[1] = 2277, sqltype.Int4
	case ArrayToString:
		inputs[0], inputs[1], inputs[2] = 2277, sqltype.Text, sqltype.Text
	}
	var err error
	if operator {
		info, err = b.BuiltinOperatorRoutineInfo(info, "||", inputs[0], inputs[1])
	} else {
		info, err = b.BuiltinRoutineInfo(info, ArrayRoutineProvider, name, inputs[:len(args)]...)
	}
	if err != nil {
		return nil, err
	}
	e := ArrayTransform{
		Info: info, Kind: kind, Args: args, Slots: slots, Labels: labels, Namespace: arrayNamespace(raw.Name), Operator: operator,
	}
	if kind == ArraySort && !targets[0].CatalogNullUsage() {
		if err := b.CaptureComparisonInput(targets[0].Type); err != nil {
			return nil, err
		}
		first := e.Formal(0)
		input := b.Info(first.ResultUsage(), raw.Pos, properties)
		var err error
		input, err = b.CollatedInfo(input, first)
		if err != nil {
			return nil, err
		}
		e.Sort, err = sqltype.BindArraySort(targets[0].Type, input.Usage.Collation, input.CollationConflict)
		if err != nil {
			return nil, err
		}
		if e.Sort.SessionDependent() {
			e.Property.Volatility = max(e.Property.Volatility, expr.Stable)
		}
	}
	return e, e.Validate()
}
