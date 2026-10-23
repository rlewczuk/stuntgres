package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindStringAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var small [2]Argument
	var names [2]string
	var operands []Argument
	var types []string
	if len(raw.Args) <= 2 {
		operands = small[:len(raw.Args)]
		types = names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
			types[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	value, separator, err := resolveStringAggregateSignature(b, raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(value.Properties(), separator.Properties())
	properties.StageDependent = true
	info, err := b.CollatedInfo(b.Info(value.ResultUsage().Type.Usage(), raw.Pos, properties), value, separator)
	if err != nil {
		return nil, err
	}
	call := AggregateCall{Provider: NewStringAggregateProvider(info.Usage), Arguments: []expr.Expr{value, separator}, Operation: AggregateString}
	formal := value.ResultUsage().Type.ID()
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, "string_agg", formal, formal)
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, info)
}

func resolveStringAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operands []Argument, types []string) (expr.Expr, expr.Expr, error) {
	if len(raw.Args) > 100 {
		return nil, nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, nil, err
	}
	if !visible || raw.Star || len(operands) != 2 {
		return nil, nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	typ := b.Type(sqltype.Text)
	for _, o := range operands {
		if d := o.Usage.Type; d != nil && d.BaseType().ID() == sqltype.Bytea {
			typ = b.Type(sqltype.Bytea)
		}
	}
	for _, o := range operands {
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, typ.Usage(), sqltype.Implicit); err != nil {
				return nil, nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
			}
		}
	}
	value, err := coerceConditional(b, operands[0], typ.Usage(), raw.Name)
	if err != nil {
		return nil, nil, err
	}
	separator, err := coerceConditional(b, operands[1], typ.Usage(), raw.Name)
	return value, separator, err
}

func windowStringAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, _, err := resolveStringAggregateSignature(b, raw, name, operands, types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	return arg, arg.ResultUsage(), nil
}
