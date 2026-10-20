package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A subscripts call retains only its analyzed arguments. Each execution opens
// fresh bounds-only state in the shared arithmetic function cursor.
type subscriptsExpression struct {
	expr.Info
	args []expr.Expr
}

func (e subscriptsExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted generate_subscripts set call")
}

func (b *expressionBinder) bindSubscripts(raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
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
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	operands := storage[:len(raw.Args)]
	signature := func() error {
		names := make([]string, len(operands))
		for i, o := range operands {
			name := "unknown"
			if d := o.descriptor(); d != nil {
				name = functions.ArrayTypeName(b, d)
			}
			names[i] = functions.NamedArgumentType(raw, i, name)
		}
		return b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, false)
	}
	if !visible || raw.Name.Parts[len(raw.Name.Parts)-1].Text != "generate_subscripts" || raw.Variadic || len(raw.ArgNames) != 0 || len(operands) != 2 && len(operands) != 3 {
		return nil, signature()
	}
	var targets [3]sqltype.Usage
	var valid bool
	targets[0], valid = subscriptInputTarget(operands[0].descriptor())
	if !valid {
		return nil, signature()
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
	args := make([]expr.Expr, len(operands))
	properties := expr.Properties{Volatility: expr.Immutable, StageDependent: true}
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
		return nil, expr.ErrorAt("42809", decoration+" specified, but generate_subscripts is "+message, raw.Pos)
	}
	if b.params.executeParameters {
		return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", raw.Pos)
	}
	if b.consumer.policyConsumer && b.query.frame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if b.clause.aggregateClause == "INSERT VALUES" {
		return nil, expr.ErrorAt("0A000", "generate_subscripts is unsupported in INSERT VALUES", raw.Pos)
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	switch b.clause.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.clause.queryHasTargetSet = true
	}
	formal := [3]sqltype.ID{sqltype.ID(2277), sqltype.Int4, sqltype.Bool}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.Int4).Usage(), raw.Pos, properties), functions.SubscriptRoutineProvider, "generate_subscripts", formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return subscriptsExpression{Info: info, args: args}, nil
}

func subscriptInputTarget(input *sqltype.Descriptor) (sqltype.Usage, bool) {
	if input == nil {
		return sqltype.Usage{}, true
	}
	base := input.BaseType()
	if !base.IsArray() {
		return sqltype.Usage{}, false
	}
	// Simple anyarray flattens an outer domain, never array element domains.
	return base.Usage(), true
}
