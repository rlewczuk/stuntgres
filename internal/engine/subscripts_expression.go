package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A subscripts call retains only its analyzed arguments. Each execution opens
// fresh bounds-only state in the shared arithmetic function cursor.
type subscriptsExpression struct {
	expressionInfo
	args []BoundExpr
}

func (e subscriptsExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted generate_subscripts set call")
}

func (e subscriptsExpression) mapChildren(mapper func(BoundExpr) (BoundExpr, bool, error)) (subscriptsExpression, bool, error) {
	changed := false
	for i, arg := range e.args {
		next, did, err := mapper(arg)
		if err != nil {
			return e, false, err
		}
		if did {
			if !changed {
				e.args = append([]BoundExpr(nil), e.args...)
			}
			e.args[i] = next
			changed = true
		}
	}
	return e, changed, nil
}

func (b *expressionBinder) bindSubscripts(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
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
	if len(raw.Args) > len(storage) {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	operands := storage[:len(raw.Args)]
	signature := func() error {
		names := make([]string, len(operands))
		for i, o := range operands {
			name := "unknown"
			if d := o.descriptor(); d != nil {
				name = b.arraySearchTypeName(d)
			}
			names[i] = functions.NamedArgumentType(raw, i, name)
		}
		return b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, false)
	}
	if !visible || raw.Name.Parts[len(raw.Name.Parts)-1].Text != "generate_subscripts" || raw.Variadic || len(raw.ArgNames) != 0 || len(operands) != 2 && len(operands) != 3 {
		return nil, signature()
	}
	var targets [3]sqltype.Usage
	if d := operands[0].descriptor(); d != nil {
		base := d.BaseType()
		if !base.IsArray() {
			return nil, signature()
		}
		// Simple anyarray flattens an outer domain, never array element domains.
		targets[0] = base.Usage()
	}
	targets[1], targets[2] = b.typ(sqltype.Int4).Usage(), b.typ(sqltype.Bool).Usage()
	// Probe all known implicit edges before contextual input conversion.
	for i, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), targets[i], sqltype.Implicit); err != nil {
				return nil, signature()
			}
		}
	}
	if targets[0].Type == nil {
		return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
	}
	args := make([]BoundExpr, len(operands))
	properties := ExprProperties{Volatility: Immutable, StageDependent: true}
	for i, o := range operands {
		arg, err := b.coerceConditional(o, targets[i], "generate_subscripts")
		if err != nil {
			return nil, err
		}
		args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
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
		return nil, expressionError("42809", decoration+" specified, but generate_subscripts is "+message, raw.Pos)
	}
	if b.executeParameters {
		return nil, expressionError("0A000", "set-returning functions are not allowed in EXECUTE parameters", raw.Pos)
	}
	if b.policyConsumer && b.queryFrame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if b.aggregateClause == "INSERT VALUES" {
		return nil, expressionError("0A000", "generate_subscripts is unsupported in INSERT VALUES", raw.Pos)
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	switch b.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.queryHasTargetSet = true
	}
	formal := [3]sqltype.ID{sqltype.ID(2277), sqltype.Int4, sqltype.Bool}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.Int4).Usage(), raw.Pos, properties), subscriptRoutineProvider, "generate_subscripts", formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return subscriptsExpression{expressionInfo: info, args: args}, nil
}
