package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindRangeAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var singleType [1]string
	types := singleType[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand Argument
	for i, child := range raw.Args {
		bound, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		operand = bound
		types[i], err = b.DomainTypeName(bound.Usage.Type)
		if err != nil {
			return nil, err
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := resolveRangeAggregateSignature(b, raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	input := arg.ResultUsage().Type
	bound, identity, err := rangeAggregateOperation(raw.Name, input)
	if err != nil {
		return nil, err
	}
	usage := bound.ResultUsage()
	provider := NewRangeAggregateProvider(input.Usage(), usage, rangeAggregateKind(raw.Name), bound)
	call := AggregateCall{Provider: provider, Arguments: []expr.Expr{arg}, Operation: identity}
	formal := sqltype.ID(3831)
	if input.IsMultirange() {
		formal = 4537
	}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, formal)
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	info := b.Info(usage, raw.Pos, expr.Properties{StageDependent: true, ParameterDependent: properties.ParameterDependent, Volatility: properties.Volatility})
	return b.FinishAggregate(raw, call, filter, info)
}

func rangeAggregateKind(name string) sqltype.RangeAggregateOperation {
	if name == "range_intersect_agg" {
		return sqltype.RangeAggregateIntersection
	}
	return sqltype.RangeAggregateUnion
}

func rangeAggregateOperation(name string, input *sqltype.Descriptor) (sqltype.RangeAggregate, AggregateOperation, error) {
	identity := AggregateRangeUnion
	if name == "range_intersect_agg" {
		identity = AggregateRangeIntersection
	}
	bound, err := sqltype.BindRangeAggregate(input, rangeAggregateKind(name))
	return bound, identity, err
}

func resolveRangeAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operand Argument, types []string) (expr.Expr, error) {
	if len(raw.Args) > 100 {
		return nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	input := operand.Usage.Type
	if input == nil {
		return nil, aggregateUnknownError(name, raw.Pos)
	}
	input = rangeAggregateInput(input)
	if input == nil {
		return nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	return coerceConditional(b, operand, input.Usage(), raw.Name)
}

func windowRangeAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, domainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, err := resolveRangeAggregateSignature(b, raw, name, firstOperand(operands), types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	bound, _, err := rangeAggregateOperation(raw.Name, arg.ResultUsage().Type)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	return arg, bound.ResultUsage(), nil
}
