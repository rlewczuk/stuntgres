package engine

import (
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type jsonKeysExpression struct {
	expressionInfo
	plan sqltype.JSONKeysPlan
	args []BoundExpr // Exactly one analyzed child; reused by the closed SRF lane.
}

func (e jsonKeysExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON keys set call")
}

func (b *expressionBinder) bindJSONFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operand comparisonOperand
	var singleName [1]string
	var names []string
	if len(raw.Args) <= 1 {
		names = singleName[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.stringOperand(r, false)
		if err != nil {
			return nil, err
		}
		if names != nil {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = b.diagnosticTypeName(d)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
		if i == 0 {
			operand = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	if !visible || len(raw.Args) != 1 || len(raw.ArgNames) != 0 {
		return missing()
	}
	id, result, keys := sqltype.JSON, sqltype.Text, false
	switch name {
	case "json_typeof":
	case "jsonb_typeof":
		id = sqltype.JSONB
	case "json_array_length":
		result = sqltype.Int4
	case "jsonb_array_length":
		id, result = sqltype.JSONB, sqltype.Int4
	case "json_object_keys":
		keys = true
	case "jsonb_object_keys":
		id, keys = sqltype.JSONB, true
	default:
		return missing()
	}
	input := b.typ(id)
	if operand.descriptor() != nil {
		if _, err := sqltype.PlanCoercion(operand.node.ResultUsage(), input.Usage(), sqltype.Implicit); err != nil {
			return missing()
		}
	}
	arg, err := b.likeOperand(operand, input)
	if err != nil {
		return nil, err
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
		return nil, expressionError("42809", decoration+" specified, but "+name+" is "+message, raw.Pos)
	}
	properties := arg.Properties()
	usage := b.typ(result).Usage()
	if keys {
		if err := b.stringSetAllowed(raw.Pos); err != nil {
			return nil, err
		}
		switch b.aggregateClause {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
			b.queryHasTargetSet = true
		}
		properties.StageDependent = true
		plan, err := sqltype.BindJSONKeys(name, arg.ResultUsage(), usage)
		if err != nil {
			return nil, err
		}
		return jsonKeysExpression{expressionInfo: b.info(usage, raw.Pos, properties), plan: plan, args: []BoundExpr{arg}}, nil
	}
	plan, err := sqltype.BindBinary(name, usage, arg.ResultUsage())
	if err != nil {
		return nil, err
	}
	return binaryExpression{expressionInfo: b.info(usage, raw.Pos, properties), plan: plan, args: []BoundExpr{arg}}, nil
}
