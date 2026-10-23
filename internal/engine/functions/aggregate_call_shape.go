package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func arrayAggregateFormal(input *sqltype.Descriptor) sqltype.ID {
	if input.BaseType().IsArray() {
		return 2277
	}
	return 2776
}

func probeArrayAggregate(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	if len(operands) != 1 {
		return false
	}
	input := operands[0].Usage.Type
	if input != nil && declared[0].Type.ID() != arrayAggregateFormal(input) {
		return false
	}
	retainAnyCallTargets(operands, declared, targets)
	return true
}

func rangeAggregateInput(input *sqltype.Descriptor) *sqltype.Descriptor {
	if input == nil {
		return nil
	}
	input = input.BaseType()
	if input.IsRange() && input.MultirangeType() != nil || input.IsMultirange() {
		return input
	}
	return nil
}

func probeRangeAggregate(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	if len(operands) != 1 {
		return false
	}
	if operands[0].Usage.Type == nil {
		targets[0] = sqltype.Usage{}
		return true
	}
	input := rangeAggregateInput(operands[0].Usage.Type)
	if input == nil || declared[0].Type.ID() != rangeCallFormal(input) {
		return false
	}
	targets[0] = input.Usage()
	return true
}

func probeAnyAggregate(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared, targets []sqltype.Usage) bool {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if name == "count" {
		if AnalyzeCountSignature(true, raw.Star, len(operands)) != CountCallable {
			return false
		}
	} else if len(declared) == 1 && declared[0].Type.ID() == 2283 {
		if ResolveAnyValue(true, raw.Star, len(operands), operands[0].Usage) == Missing {
			return false
		}
	}
	retainAnyCallTargets(operands, declared, targets)
	return true
}
