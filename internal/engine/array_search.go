package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type arraySearchExpression struct {
	expressionInfo
	plan      sqltype.ArraySearchPlan
	args      []BoundExpr
	namespace string
}

func (e arraySearchExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.plan.Evaluate(c.Context, values[:len(e.args)], c.Input)
}

func arrayMembershipName(name string) bool { return name == "@>" || name == "<@" || name == "&&" }

func (b *expressionBinder) bindArraySearch(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operands := make([]comparisonOperand, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.comparisonOperandPolicy(arg, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		operands[i] = o
		names[i] = "unknown"
		if d := o.descriptor(); d != nil {
			names[i] = b.arraySearchTypeName(d)
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
		return nil, err
	}
	signature := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	arity := 2
	if name == "array_replace" {
		arity = 3
	}
	if !visible || name != strings.ToLower(name) || len(raw.ArgNames) != 0 || len(operands) != arity && !(name == "array_position" && len(operands) == 3) {
		return signature()
	}
	views := append([]comparisonOperand(nil), operands[:arity]...)
	if d := operands[0].descriptor(); d != nil {
		base := d.BaseType()
		if !base.IsArray() {
			return signature()
		}
		u := base.Element().Usage()
		u.Typmod = operands[0].node.ResultUsage().Typmod
		u.Collation = operands[0].node.ResultUsage().Collation
		views[0].node = literalExpression{expressionInfo: expressionInfo{usage: u}}
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
	array, ok := b.bindingCatalog().LookupArray(common.Type)
	if !ok || !array.Concrete() {
		return signature()
	}
	targets := []sqltype.Usage{array.Usage(), common}
	if name == "array_replace" {
		targets = append(targets, common)
	}
	if name == "array_position" && len(operands) == 3 {
		targets = append(targets, b.typ(sqltype.Int4).Usage())
	}
	// Function resolution precedes literal input conversion. Probe every known
	// implicit edge before finalizing any unknown literal or Param occurrence.
	for i, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), targets[i], sqltype.Implicit); err != nil {
				return signature()
			}
		}
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
	args := make([]BoundExpr, len(operands))
	for i, o := range operands {
		args[i], err = b.coerceConditional(o, targets[i], name)
		if err != nil {
			return nil, err
		}
	}
	result := array.Usage()
	if name == "array_position" {
		result = b.typ(sqltype.Int4).Usage()
	}
	if name == "array_positions" {
		positions, ok := b.bindingCatalog().LookupArray(b.typ(sqltype.Int4))
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
	plan, err := sqltype.BindArraySearch(name, args[0].ResultUsage().Type.BaseType(), result.Type, b.typ(sqltype.Int4), input.usage.Collation, input.collationConflict)
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
	namespace := ""
	if len(rawName.Parts) == 2 {
		namespace = rawName.Parts[0].Text
	}
	return arraySearchExpression{expressionInfo: info, plan: plan, args: args, namespace: namespace}, nil
}

func (e arraySearchExpression) sql(child func(BoundExpr) (string, error), operator func(string, string) string) (string, error) {
	parts := make([]string, len(e.args))
	for i, arg := range e.args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if e.plan.Membership() {
		return "(" + parts[0] + " " + operator(e.namespace, e.plan.Name()) + " " + parts[1] + ")", nil
	}
	return "pg_catalog." + e.plan.Name() + "(" + strings.Join(parts, ", ") + ")", nil
}

func mapArraySearchChildren(e arraySearchExpression, child func(BoundExpr) (BoundExpr, bool, error)) (arraySearchExpression, bool, error) {
	var args []BoundExpr
	for i, old := range e.args {
		next, changed, err := child(old)
		if err != nil {
			return e, false, err
		}
		if changed {
			if args == nil {
				args = append([]BoundExpr(nil), e.args...)
			}
			args[i] = next
		}
	}
	if args == nil {
		return e, false, nil
	}
	e.args = args
	return e, true, nil
}
