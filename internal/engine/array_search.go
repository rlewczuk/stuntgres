package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func arrayMembershipName(name string) bool { return name == "@>" || name == "<@" || name == "&&" }

// bindArraySearch binds the anycompatible array search functions. Visibility
// is resolved before argument analysis.
func bindArraySearch(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operands, err := functions.AnalyzeArraySearch(b, raw.Args)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(raw.Args))
	for i, o := range operands {
		names[i] = "unknown"
		if d := o.Usage.Type; d != nil {
			names[i] = functions.ArrayTypeName(b, d)
		}
		names[i] = functions.NamedArgumentType(raw, i, names[i])
	}
	if err := b.AnalyzeFilter(raw.Filter); err != nil {
		return nil, err
	}
	signature := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	arity, valid := functions.ArraySearchArity(name, visible, len(raw.ArgNames) != 0, len(operands))
	if !valid {
		return signature()
	}
	// anycompatible resolution collects known leaf OIDs before selecting a
	// common type; UNKNOWN arguments must not erase an agreed domain identity.
	var known [3]sqltype.Usage
	count := 0
	for i, argument := range operands[:arity] {
		u := argument.Usage
		if i == 0 && u.Type != nil {
			u, valid = functions.ArraySearchLeafUsage(u)
			if !valid {
				return signature()
			}
		}
		if u.Type != nil {
			known[count] = u
			count++
		}
	}
	common := b.CommonUsage(known[:count])
	if common.Type == nil {
		return signature()
	}
	array, ok := b.ArrayType(common.Type)
	if !ok || !array.Concrete() {
		return signature()
	}
	var integer sqltype.Usage
	if name == "array_position" && len(operands) == 3 {
		integer = b.Type(sqltype.Int4).Usage()
	}
	targets, valid := functions.SelectArraySearch(name, operands, array.Usage(), common, integer)
	if !valid {
		return signature()
	}
	switch {
	case raw.Star:
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expr.ErrorAt("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expr.ErrorAt("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expr.ErrorAt("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expr.ErrorAt("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	args, err := functions.FinishArraySearch(b, name, operands, targets)
	if err != nil {
		return nil, err
	}
	result := array.Usage()
	if name == "array_position" {
		result = b.Type(sqltype.Int4).Usage()
	}
	if name == "array_positions" {
		positions, ok := b.ArrayType(b.Type(sqltype.Int4))
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing integer array association")
		}
		result = positions.Usage()
	}
	return arraySearchNode(b, name, raw.Name, raw.Pos, result, args)
}

func (b *expressionBinder) arrayMembershipError(left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	err := b.comparisonError(sqlError("42883", "operator does not exist"), left, right, raw)
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", functions.ArrayTypeName(b, left), objectNameText(raw.Name), functions.ArrayTypeName(b, right))
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
	return arraySearchNode(b, raw.Name.Parts[len(raw.Name.Parts)-1].Text, raw.Name, raw.Pos, b.typ(sqltype.Bool).Usage(), args)
}

// arraySearchNode builds an array search or membership node over coerced
// arguments, capturing the selected routine identity.
func arraySearchNode(b functions.Binder, name string, rawName parser.ObjectName, pos parser.Position, result sqltype.Usage, args []expr.Expr) (expr.Expr, error) {
	input, err := b.ComparisonInfo(args[0], args[1])
	if err != nil {
		return nil, err
	}
	if name == "array_replace" {
		input, err = b.CollatedInfo(input, args[2])
		if err != nil {
			return nil, err
		}
	}
	plan, err := sqltype.BindArraySearch(name, args[0].ResultUsage().Type.BaseType(), result.Type, b.Type(sqltype.Int4), input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, err
	}
	properties := expr.Properties{}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, expr.Stable)
	}
	info := b.Info(result, pos, properties)
	if result.Collation.ID != 0 {
		info, err = b.CollatedInfo(info, args...)
		if err != nil {
			return nil, err
		}
	}
	routineName, provider := name, functions.ArrayRoutineProvider
	formal := [3]sqltype.ID{sqltype.ID(5078), sqltype.ID(5077)}
	if name == "array_position" && len(args) == 3 {
		formal[2] = sqltype.Int4
	} else if name == "array_replace" {
		formal[2] = sqltype.ID(5077)
	} else if plan.Membership() {
		provider = functions.OperatorRoutineProvider
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
	info, err = b.BuiltinRoutineInfo(info, provider, routineName, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return functions.NewArraySearch(info, plan, args, arrayNamespace(rawName)), nil
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
