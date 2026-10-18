package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// arraySearchNames are the anycompatible array search functions.
var arraySearchNames = [...]string{"array_position", "array_positions", "array_remove", "array_replace"}

// bindArraySearch binds the anycompatible array search functions. Visibility
// is resolved before argument analysis.
func bindArraySearch(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operands, err := AnalyzeArraySearch(b, raw.Args)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(raw.Args))
	for i, o := range operands {
		names[i] = "unknown"
		if d := o.Usage.Type; d != nil {
			names[i] = ArrayTypeName(b, d)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	if err := b.AnalyzeFilter(raw.Filter); err != nil {
		return nil, err
	}
	signature := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	arity, valid := ArraySearchArity(name, visible, len(raw.ArgNames) != 0, len(operands))
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
			u, valid = ArraySearchLeafUsage(u)
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
	targets, valid := SelectArraySearch(name, operands, array.Usage(), common, integer)
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
	args, err := FinishArraySearch(b, name, operands, targets)
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
	return ArraySearchNode(b, name, raw.Name, raw.Pos, result, args)
}

// ArraySearchNode builds an array search or membership node over coerced
// arguments, capturing the selected routine identity.
func ArraySearchNode(b Binder, name string, rawName parser.ObjectName, pos parser.Position, result sqltype.Usage, args []expr.Expr) (expr.Expr, error) {
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
	routineName, provider := name, ArrayRoutineProvider
	formal := [3]sqltype.ID{sqltype.ID(5078), sqltype.ID(5077)}
	if name == "array_position" && len(args) == 3 {
		formal[2] = sqltype.Int4
	} else if name == "array_replace" {
		formal[2] = sqltype.ID(5077)
	} else if plan.Membership() {
		provider = OperatorRoutineProvider
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
	return NewArraySearch(info, plan, args, arrayNamespace(rawName)), nil
}
