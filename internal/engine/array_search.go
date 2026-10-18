package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func arrayMembershipName(name string) bool { return name == "@>" || name == "<@" || name == "&&" }

func (b *expressionBinder) bindArraySearch(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operands, err := functions.AnalyzeArraySearch(b, raw.Args)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(raw.Args))
	for i, o := range operands {
		names[i] = "unknown"
		if d := o.Usage.Type; d != nil {
			names[i] = b.arraySearchTypeName(d)
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
		return nil, err
	}
	signature := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	arity, valid := functions.ArraySearchArity(name, visible, len(raw.ArgNames) != 0, len(operands))
	if !valid {
		return signature()
	}
	views := make([]comparisonOperand, arity)
	for i, argument := range operands[:arity] {
		views[i] = comparisonOperand{node: argument.Expression, raw: argument.Raw}
	}
	if operands[0].Usage.Type != nil {
		u, valid := functions.ArraySearchLeafUsage(operands[0].Usage)
		if !valid {
			return signature()
		}
		views[0].node = literalExpression{expressionInfo: expressionInfo{Usage: u}}
	}
	// anycompatible resolution collects known leaf OIDs before selecting a
	// common type; UNKNOWN arguments must not erase an agreed domain identity.
	known := views[:0]
	for _, view := range views {
		if view.descriptor() != nil {
			known = append(known, view)
		}
	}
	views = known
	common, err := selectCommonUsage(len(views), func(i int) comparisonOperand { return views[i] }, b.typ(sqltype.Text).Usage(), "", b.diagnosticTypeName)
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
	var integer sqltype.Usage
	if name == "array_position" && len(operands) == 3 {
		integer = b.typ(sqltype.Int4).Usage()
	}
	targets, valid := functions.SelectArraySearch(name, operands, array.Usage(), common, integer)
	if !valid {
		return signature()
	}
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	args, err := functions.FinishArraySearch(b, name, operands, targets)
	if err != nil {
		return nil, err
	}
	result := array.Usage()
	if name == "array_position" {
		result = b.typ(sqltype.Int4).Usage()
	}
	if name == "array_positions" {
		positions, ok := b.definitionArray(b.typ(sqltype.Int4))
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing integer array association")
		}
		result = positions.Usage()
	}
	return b.newArraySearch(name, raw.Name, raw.Pos, result, args)
}

// Array signatures display the visible leaf name, not the internal array
// descriptor's frozen qualified declaration. Scalar naming remains catalog-owned.
func (b *expressionBinder) arraySearchTypeName(d *sqltype.Descriptor) string {
	if d != nil && d.IsArray() {
		return b.diagnosticTypeName(d.Element()) + "[]"
	}
	return b.diagnosticTypeName(d)
}

func (b *expressionBinder) arrayMembershipError(left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	err := b.comparisonError(sqlError("42883", "operator does not exist"), left, right, raw)
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", b.arraySearchTypeName(left), objectNameText(raw.Name), b.arraySearchTypeName(right))
		return &copy
	}
	return err
}

func (b *expressionBinder) bindArrayMembership(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 {
		return nil, fmt.Errorf("stuntgres: invalid membership arity")
	}
	var operands [2]comparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	return b.finishArrayMembership(raw, operands)
}

func (b *expressionBinder) finishArrayMembership(raw parser.OperatorExpr, operands [2]comparisonOperand) (BoundExpr, error) {
	left, right := operands[0].descriptor(), operands[1].descriptor()
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, b.arrayMembershipError(left, right, raw)
	}
	if left == nil && right == nil {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		d := expressionError("42725", "operator is not unique: unknown "+name+" unknown", raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
		return nil, d
	}
	if jsonbContainmentName(raw.Name.Parts[len(raw.Name.Parts)-1].Text) &&
		(left != nil && left.BaseType().ID() == sqltype.JSONB || right != nil && right.BaseType().ID() == sqltype.JSONB) {
		return b.bindJSONBContainment(raw, operands)
	}
	// ACL arrays select the fixed array/item signature for an unknown RHS;
	// a known array RHS still belongs to the generic array/array operator.
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text == "@>" && (right != nil && right.BaseType().ID() == sqltype.ACLItem && (left == nil || left.BaseType().ID() == sqltype.ACLItemArray) || right == nil && left != nil && left.BaseType().ID() == sqltype.ACLItemArray) {
		n := aclValueExpression{kind: aclContains}
		properties := n.baseProperties()
		for i, typ := range n.kind.types() {
			arg, err := b.coerceConditional(operands[i], b.typ(typ).Usage(), "@>")
			if err != nil {
				return nil, err
			}
			n.args[i] = arg
			properties = mergeExpressionProperties(properties, arg.Properties())
		}
		if len(raw.Name.Parts) == 2 {
			n.namespace = raw.Name.Parts[0].Text
		}
		n.expressionInfo = b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties)
		n.expressionInfo, err = b.builtinOperatorRoutineInfo(n.expressionInfo, "@>", sqltype.ACLItemArray, sqltype.ACLItem)
		if err != nil {
			return nil, err
		}
		return n, nil
	}
	var selected *sqltype.Descriptor
	for _, d := range []*sqltype.Descriptor{left, right} {
		if d == nil {
			continue
		}
		base := d.BaseType()
		if !base.IsArray() || selected != nil && selected != base {
			return nil, b.arrayMembershipError(left, right, raw)
		}
		selected = base
	}
	args := make([]BoundExpr, 2)
	for i, o := range operands {
		args[i], err = b.coerceConditional(o, selected.Usage(), raw.Name.Parts[len(raw.Name.Parts)-1].Text)
		if err != nil {
			return nil, err
		}
	}
	return b.newArraySearch(raw.Name.Parts[len(raw.Name.Parts)-1].Text, raw.Name, raw.Pos, b.typ(sqltype.Bool).Usage(), args)
}

func (b *expressionBinder) newArraySearch(name string, rawName parser.ObjectName, pos parser.Position, result sqltype.Usage, args []BoundExpr) (BoundExpr, error) {
	input, err := b.comparisonInfo(args[0], args[1])
	if err != nil {
		return nil, err
	}
	if name == "array_replace" {
		input, err = b.collatedInfo(input, args[2])
		if err != nil {
			return nil, err
		}
	}
	plan, err := sqltype.BindArraySearch(name, args[0].ResultUsage().Type.BaseType(), result.Type, b.typ(sqltype.Int4), input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, err
	}
	properties := ExprProperties{}
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, Stable)
	}
	info := b.info(result, pos, properties)
	if result.Collation.ID != 0 {
		info, err = b.collatedInfo(info, args...)
		if err != nil {
			return nil, err
		}
	}
	routineName, provider := name, arrayRoutineProvider
	formal := [3]sqltype.ID{sqltype.ID(5078), sqltype.ID(5077)}
	if name == "array_position" && len(args) == 3 {
		formal[2] = sqltype.Int4
	} else if name == "array_replace" {
		formal[2] = sqltype.ID(5077)
	} else if plan.Membership() {
		provider = operatorRoutineProvider
		formal[0], formal[1] = sqltype.ID(2277), sqltype.ID(2277)
		switch name {
		case "&&":
			routineName = "arrayoverlap"
		case "@>":
			routineName = "arraycontains"
		case "<@":
			routineName = "arraycontained"
		}
	}
	info, err = b.builtinRoutineInfo(info, provider, routineName, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(rawName.Parts) == 2 {
		namespace = rawName.Parts[0].Text
	}
	return functions.NewArraySearch(functionInfo(info), plan, args, namespace), nil
}

func arraySearchSQL(e functions.ArraySearch, child func(BoundExpr) (string, error), operator func(string, string) string) (string, error) {
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if e.Plan.Membership() {
		return "(" + parts[0] + " " + operator(e.Namespace, e.Plan.Name()) + " " + parts[1] + ")", nil
	}
	return "pg_catalog." + e.Plan.Name() + "(" + strings.Join(parts, ", ") + ")", nil
}

func mapArraySearchChildren(e functions.ArraySearch, child func(BoundExpr) (BoundExpr, bool, error)) (functions.ArraySearch, bool, error) {
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
