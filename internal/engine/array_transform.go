package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindArrayTransform binds the array transform functions. Named arguments
// are admitted only for array_sort's three formals.
func bindArrayTransform(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
			o, err := b.AnalyzeArgument(key.Expr, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
			if err != nil {
				return nil, err
			}
			operands = append(operands, o)
		}
		return signature()
	}
	kind := functions.ArrayTransformFunction(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !visible || !kind.Arity(len(operands)) {
		return signature()
	}
	slots := [3]uint8{0, 1, 2}
	labels := [3]string{}
	if len(raw.ArgNames) != 0 {
		if kind != functions.ArraySort || len(operands) == 1 {
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
	return finishArrayTransform(b, raw, kind, operands, slots, labels, false, signature)
}

// finishArrayTransform selects and coerces a transform's formals. The ||
// operator reaches it in operator mode with already analyzed operands.
func finishArrayTransform(b functions.Binder, raw parser.FunctionExpr, kind functions.ArrayTransformKind, operands []functions.Argument, slots [3]uint8, labels [3]string, operator bool, signature func() (expr.Expr, error)) (expr.Expr, error) {
	var formal [3]functions.Argument
	for i, o := range operands {
		formal[slots[i]] = o
	}
	var targets [3]sqltype.Usage
	var result sqltype.Usage
	compatible := kind == functions.ArrayAppend || kind == functions.ArrayPrepend || kind == functions.ArrayConcatenate
	if compatible {
		// Collect known logical leaves first: an UNKNOWN argument must not erase
		// the domain shared by all known inputs.
		var views [2]sqltype.Usage
		count := 0
		for i := range operands {
			u := formal[i].Usage
			arrayArg := kind == functions.ArrayConcatenate || kind == functions.ArrayAppend && i == 0 || kind == functions.ArrayPrepend && i == 1
			if arrayArg {
				var ok bool
				u, ok = functions.ArraySearchLeafUsage(u)
				if !ok {
					return signature()
				}
			}
			if u.Type != nil {
				views[count] = u
				count++
			}
		}
		common := b.CommonUsage(views[:count])
		if common.Type == nil {
			return signature()
		}
		array, ok := b.ArrayType(common.Type)
		if !ok || !array.Concrete() {
			return signature()
		}
		result = array.Usage()
		result.Collation = common.Collation
		for i := range operands {
			targets[i] = common
			if kind == functions.ArrayConcatenate || kind == functions.ArrayAppend && i == 0 || kind == functions.ArrayPrepend && i == 1 {
				targets[i] = result
			}
		}
	} else {
		u := formal[0].Usage
		if kind == functions.ArrayFill {
			if u.Type != nil {
				array, ok := b.ArrayType(u.Type)
				if !ok {
					return nil, expr.ErrorAt("42704", "could not find array type for data type "+functions.ArrayTypeName(b, u.Type), parser.Position{})
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
			if u.Type != nil {
				if u.CatalogNullUsage() {
					targets[0] = u
				} else {
					if !u.Type.BaseType().IsArray() {
						return signature()
					}
					targets[0] = u.Type.BaseType().Usage()
				}
				result = targets[0]
			}
			switch kind {
			case functions.ArraySort:
				targets[1], targets[2] = b.Type(sqltype.Bool).Usage(), b.Type(sqltype.Bool).Usage()
			case functions.ArrayTrim:
				targets[1] = b.Type(sqltype.Int4).Usage()
			case functions.ArrayToString:
				result = b.Type(sqltype.Text).Usage()
				targets[1], targets[2] = result, result
			}
		}
	}
	// Probe every known edge before importing any unknown literal or parameter.
	for i := range operands {
		u := formal[i].Usage
		if u.Type != nil && !u.CatalogNullUsage() {
			if targets[i].Type == nil {
				return signature()
			}
			if _, err := sqltype.PlanCoercion(u, targets[i], sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	name := kind.Name()
	if !operator {
		if raw.Star {
			return nil, expr.ErrorAt("42809", name+"(*) specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
		}
		if err := functions.CallDecorationError(raw); err != nil {
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
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: target, Label: name})
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if kind == functions.ArrayToString {
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
	case functions.ArrayAppend:
		inputs[0], inputs[1] = 5078, 5077
	case functions.ArrayPrepend:
		inputs[0], inputs[1] = 5077, 5078
	case functions.ArrayConcatenate:
		inputs[0], inputs[1] = 5078, 5078
	case functions.ArrayFill:
		inputs[0], inputs[1], inputs[2] = 2283, 1007, 1007
	case functions.ArrayReverse:
		inputs[0] = 2277
	case functions.ArraySort:
		inputs[0], inputs[1], inputs[2] = 2277, sqltype.Bool, sqltype.Bool
	case functions.ArrayTrim:
		inputs[0], inputs[1] = 2277, sqltype.Int4
	case functions.ArrayToString:
		inputs[0], inputs[1], inputs[2] = 2277, sqltype.Text, sqltype.Text
	}
	var err error
	if operator {
		info, err = b.BuiltinOperatorRoutineInfo(info, "||", inputs[0], inputs[1])
	} else {
		info, err = b.BuiltinRoutineInfo(info, functions.ArrayRoutineProvider, name, inputs[:len(args)]...)
	}
	if err != nil {
		return nil, err
	}
	e := functions.ArrayTransform{
		Info: info, Kind: kind, Args: args, Slots: slots, Labels: labels, Namespace: arrayNamespace(raw.Name), Operator: operator,
	}
	if kind == functions.ArraySort && !targets[0].CatalogNullUsage() {
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

// This is the array branch of the existing || selector. Its children have
// already been analyzed by bindStringConcat; no child is rebound here.
func (b *expressionBinder) selectArrayConcat(raw parser.OperatorExpr, operands [2]functions.Argument, visible bool) (BoundExpr, bool, error) {
	left, right := operands[0].Usage.Type, operands[1].Usage.Type
	array := func(d *sqltype.Descriptor) bool { return d != nil && d.BaseType().IsArray() }
	if !array(left) && !array(right) {
		return nil, false, nil
	}
	signature := func() (BoundExpr, error) { return nil, b.arrayMembershipError(left, right, raw) }
	if !visible {
		e, err := signature()
		return e, true, err
	}
	kind := functions.ArrayConcatenate
	if array(left) && right != nil && !array(right) {
		kind = functions.ArrayAppend
	}
	if array(right) && left != nil && !array(left) {
		kind = functions.ArrayPrepend
	}
	if array(left) && array(right) {
		if right.BaseType().Element() == left {
			kind = functions.ArrayPrepend
		} else if left.BaseType().Element() == right {
			kind = functions.ArrayAppend
		}
	}
	call := parser.FunctionExpr{Name: raw.Name, Pos: raw.Pos}
	e, err := finishArrayTransform(b, call, kind, operands[:], [3]uint8{0, 1, 2}, [3]string{}, true, signature)
	return e, true, err
}

func mapArrayTransformChildren(e functions.ArrayTransform, child func(BoundExpr) (BoundExpr, bool, error)) (functions.ArrayTransform, bool, error) {
	var args []BoundExpr
	// Simplification/preflight may evaluate immutable children and fail. Preserve
	// formal evaluation precedence there too, without changing written slots.
	for slot := range e.Args {
		for i, old := range e.Args {
			if int(e.Slots[i]) != slot {
				continue
			}
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
	}
	if args == nil {
		return e, false, nil
	}
	e.Args = args
	return e, true, nil
}

func arrayTransformSQL(e functions.ArrayTransform, child func(BoundExpr) (string, error), operator func(string, string) string) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = "\"" + e.Labels[i] + "\" => " + text
		}
		parts[i] = text
	}
	if e.Operator {
		return "(" + parts[0] + " " + operator(e.Namespace, "||") + " " + parts[1] + ")", nil
	}
	return "pg_catalog." + e.Kind.Name() + "(" + strings.Join(parts, ", ") + ")", nil
}
