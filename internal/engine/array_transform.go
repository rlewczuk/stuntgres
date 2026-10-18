package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindArrayTransform(raw parser.FunctionExpr) (BoundExpr, error) {
	operands := make([]comparisonOperand, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.comparisonOperandPolicy(arg, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
		return nil, err
	}
	if len(operands) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return nil, fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return nil, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = b.arraySearchTypeName(d)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if raw.Within {
		// Ordered-set lookup includes the ORDER BY inputs in the signature.
		// No selected transform has an ordered-set overload.
		for _, key := range raw.Order {
			o, err := b.comparisonOperandPolicy(key.Expr, expressionPolicy{context: projectionContext})
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
	return b.finishArrayTransform(raw, kind, operands, slots, labels, false, signature)
}

func (b *expressionBinder) finishArrayTransform(raw parser.FunctionExpr, kind functions.ArrayTransformKind, operands []comparisonOperand, slots [3]uint8, labels [3]string, operator bool, signature func() (BoundExpr, error)) (BoundExpr, error) {
	var formal [3]comparisonOperand
	for i, o := range operands {
		formal[slots[i]] = o
	}
	var targets [3]sqltype.Usage
	var result sqltype.Usage
	compatible := kind == functions.ArrayAppend || kind == functions.ArrayPrepend || kind == functions.ArrayConcatenate
	if compatible {
		// Collect known logical leaves first: an UNKNOWN argument must not erase
		// the domain shared by all known inputs.
		var views [2]comparisonOperand
		count := 0
		for i := range operands {
			o := formal[i]
			u := operandUsage(o)
			arrayArg := kind == functions.ArrayConcatenate || kind == functions.ArrayAppend && i == 0 || kind == functions.ArrayPrepend && i == 1
			if arrayArg {
				var ok bool
				u, ok = functions.ArraySearchLeafUsage(u)
				if !ok {
					return signature()
				}
			}
			if u.Type != nil {
				views[count] = comparisonOperand{node: literalExpression{expressionInfo: expressionInfo{Usage: u}}}
				count++
			}
		}
		common, err := selectCommonUsage(count, func(i int) comparisonOperand { return views[i] }, b.typ(sqltype.Text).Usage(), "", b.diagnosticTypeName)
		if err != nil {
			return nil, err
		}
		if common.Type == nil {
			return signature()
		}
		array, ok := b.definitionArray(common.Type)
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
		u := operandUsage(formal[0])
		if kind == functions.ArrayFill {
			if u.Type != nil {
				array, ok := b.definitionArray(u.Type)
				if !ok {
					return nil, sqlError("42704", "could not find array type for data type "+b.arraySearchTypeName(u.Type))
				}
				if !array.Concrete() {
					return signature()
				}
				result = array.Usage()
				targets[0] = u
			}
			integer, ok := b.definitionArray(b.typ(sqltype.Int4))
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
				targets[1], targets[2] = b.typ(sqltype.Bool).Usage(), b.typ(sqltype.Bool).Usage()
			case functions.ArrayTrim:
				targets[1] = b.typ(sqltype.Int4).Usage()
			case functions.ArrayToString:
				result = b.typ(sqltype.Text).Usage()
				targets[1], targets[2] = result, result
			}
		}
	}
	// Probe every known edge before importing any unknown literal or parameter.
	for i := range operands {
		u := operandUsage(formal[i])
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
		decoration, message := "", "not an aggregate function"
		switch {
		case raw.Star:
			decoration = name + "(*)"
		case raw.Distinct:
			decoration = "DISTINCT"
		case len(raw.Order) != 0:
			decoration = "ORDER BY"
		case raw.Filter != nil:
			decoration = "FILTER"
		case raw.Window != nil:
			decoration, message = "OVER", "not a window function nor an aggregate function"
		}
		if decoration != "" {
			return nil, expressionError("42809", decoration+" specified, but "+objectNameText(raw.Name)+" is "+message, raw.Pos)
		}
	}
	if targets[0].Type == nil {
		return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
	}
	args := make([]BoundExpr, len(operands))
	properties := ExprProperties{}
	for i, o := range operands {
		target := targets[slots[i]]
		arg := o.node
		if !target.CatalogNullUsage() {
			var err error
			arg, err = b.coerceConditional(o, target, name)
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if kind == functions.ArrayToString {
		properties.Volatility = max(properties.Volatility, Stable)
		if targets[0].Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
	}
	info := b.info(result, raw.Pos, properties)
	if result.Collation.ID != 0 {
		var err error
		info, err = b.collatedInfo(info, args...)
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
		info, err = b.builtinOperatorRoutineInfo(info, "||", inputs[0], inputs[1])
	} else {
		info, err = b.builtinRoutineInfo(info, arrayRoutineProvider, name, inputs[:len(args)]...)
	}
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	e := functions.ArrayTransform{
		Info: functionInfo(info), Kind: kind, Args: args, Slots: slots, Labels: labels, Namespace: namespace, Operator: operator,
	}
	if kind == functions.ArraySort && !targets[0].CatalogNullUsage() {
		if err := b.captureComparisonDiagnosticInput(targets[0].Type); err != nil {
			return nil, err
		}
		first := e.Formal(0)
		input := b.info(first.ResultUsage(), raw.Pos, properties)
		var err error
		input, err = b.collatedInfo(input, first)
		if err != nil {
			return nil, err
		}
		e.Sort, err = sqltype.BindArraySort(targets[0].Type, input.Usage.Collation, input.CollationConflict)
		if err != nil {
			return nil, err
		}
		if e.Sort.SessionDependent() {
			e.Property.Volatility = max(e.Property.Volatility, Stable)
		}
	}
	return e, e.Validate()
}

func operandUsage(o comparisonOperand) sqltype.Usage {
	if o.node == nil {
		return sqltype.Usage{}
	}
	return o.node.ResultUsage()
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
	views := []comparisonOperand{
		{node: operands[0].Expression, raw: operands[0].Raw},
		{node: operands[1].Expression, raw: operands[1].Raw},
	}
	call := parser.FunctionExpr{Name: raw.Name, Pos: raw.Pos}
	e, err := b.finishArrayTransform(call, kind, views, [3]uint8{0, 1, 2}, [3]string{}, true, signature)
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
