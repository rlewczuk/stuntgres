package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindJSONFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operand functions.Argument[BoundExpr]
	var singleName [1]string
	var names []string
	if len(raw.Args) <= 1 {
		names = singleName[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, functions.AnalysisRequest{Intent: functions.StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if names != nil {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = b.diagnosticTypeName(d)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
		if i == 0 {
			operand = o
		}
	}
	if name == "json_object_keys" || name == "json_each" || name == "json_each_text" {
		if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
			return nil, err
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	namedCount := len(raw.ArgNames)
	if (name == "json_each" || name == "json_each_text") && namedCount == 1 && raw.ArgNames[0].Text == "from_json" {
		namedCount = 0
	}
	definition, resolution := functions.JSONFunctionCall(name, visible, len(raw.Args), namedCount)
	if resolution != functions.Resolved {
		return missing()
	}
	input := b.typ(definition.Input)
	if functions.ResolveJSONArgument(operand.Usage, input.Usage()) != functions.Resolved {
		return missing()
	}
	arg, err := b.CoerceArgument(operand, functions.CoercionRequest{Intent: definition.ArgumentIntent, Target: input.Usage()})
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
		diagnosticName := name
		if name == "json_object_keys" {
			diagnosticName = objectNameText(raw.Name)
		}
		return nil, expressionError("42809", decoration+" specified, but "+diagnosticName+" is "+message, raw.Pos)
	}
	properties := arg.Properties()
	usage := b.typ(definition.Result).Usage()
	if definition.Each {
		value := input.Usage()
		if name == "json_each_text" {
			value = b.typ(sqltype.Text).Usage()
		}
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "key", Usage: b.typ(sqltype.Text).Usage()},
			{Attribute: 2, Name: "value", Usage: value},
		})
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	if definition.Keys || definition.Each {
		if err := b.stringSetAllowed(raw.Pos); err != nil {
			return nil, err
		}
		switch b.aggregateClause {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
			b.queryHasTargetSet = true
		}
		properties.StageDependent = true
		if definition.Each {
			return functions.NewJSONEach[BoundExpr, *EvalContext](functionInfo(b.info(usage, raw.Pos, properties)), name, arg)
		}
		return functions.NewJSONKeys[BoundExpr, *EvalContext](functionInfo(b.info(usage, raw.Pos, properties)), name, arg)
	}
	return functions.NewBinary[BoundExpr, *EvalContext](functionInfo(b.info(usage, raw.Pos, properties)), name, []BoundExpr{arg}, parser.OrdinaryFunctionSyntax)
}
