package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindArrayInspect(raw parser.FunctionExpr) (BoundExpr, error) {
	var storage [100]comparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperandPolicy(arg, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		if i < len(storage) {
			storage[i] = o
		}
	}
	if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
		return nil, err
	}
	if len(raw.Args) > len(storage) {
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
	// Resolve visibility only after child/FILTER analysis and label validation.
	// The already analyzed operands are retained; no child is rebound.
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	operands := storage[:len(raw.Args)]
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			name := "unknown"
			if d := o.descriptor(); d != nil {
				name = b.arraySearchTypeName(d)
			}
			names[i] = functionNamedType(raw, i, name)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := functions.ArrayInspectFunction(name)
	if !visible || kind == 0 || len(raw.ArgNames) != 0 || len(operands) != kind.Arity() {
		return signature()
	}
	var targets [2]sqltype.Usage
	catalogNull := false
	if d := operands[0].descriptor(); d != nil {
		u := operands[0].node.ResultUsage()
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
	targets[1] = b.typ(sqltype.Int4).Usage()
	for i, o := range operands {
		if o.descriptor() != nil && !(i == 0 && catalogNull) {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), targets[i], sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	decoration, message := "", "not an aggregate function"
	switch {
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
	if targets[0].Type == nil {
		return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
	}
	args := make([]BoundExpr, len(operands))
	properties := ExprProperties{}
	for i, o := range operands {
		arg := o.node
		if !(i == 0 && catalogNull) {
			var err error
			arg, err = b.coerceConditional(o, targets[i], name)
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info := b.info(b.typ(kind.Result()).Usage(), raw.Pos, properties)
	var formal [2]sqltype.ID
	formal[0], formal[1] = 2277, sqltype.Int4
	info, err = b.builtinRoutineInfo(info, arrayRoutineProvider, kind.Name(), formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	e := functions.ArrayInspect[BoundExpr, ExprContext, ExprProperties]{Info: functionInfo(info), Kind: kind, Args: args, Namespace: namespace}
	return e, e.Validate()
}

func mapArrayInspectChildren(e functions.ArrayInspect[BoundExpr, ExprContext, ExprProperties], child func(BoundExpr) (BoundExpr, bool, error)) (functions.ArrayInspect[BoundExpr, ExprContext, ExprProperties], bool, error) {
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

func arrayInspectSQL(e functions.ArrayInspect[BoundExpr, ExprContext, ExprProperties], child func(BoundExpr) (string, error)) (string, error) {
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
