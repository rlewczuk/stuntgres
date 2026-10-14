package engine

import (
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindArrayAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var small [1]string
	var types []string
	if len(raw.Args) <= 1 {
		types = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	var operand comparisonOperand
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		operand = o
		if types != nil {
			types[i] = b.diagnosticTypeName(o.descriptor())
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, usage, arrays, err := b.resolveArrayAggregateSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	properties.StageDependent = true
	info := b.info(usage, raw.Pos, properties)
	info.collationConflict, info.collationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	operation := aggregateArrayScalar
	if arrays {
		operation = aggregateArrayArray
	}
	call := aggregateCall{
		provider:     functions.NewArrayAggregateProvider(arg.ResultUsage().Type, usage, arrays),
		argument:     arg,
		operation:    operation,
		includeNulls: true,
	}
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) resolveArrayAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, sqltype.Usage, bool, error) {
	if len(raw.Args) > 100 {
		return nil, sqltype.Usage{}, false, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, sqltype.Usage{}, false, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, sqltype.Usage{}, false, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	if !operand.descriptor().Concrete() {
		return nil, sqltype.Usage{}, false, functionResolutionErrorForTypes(name, raw.Pos, types, true)
	}
	arg := operand.node
	usage := arg.ResultUsage()
	usage.Typmod = -1
	arrays := usage.Type.BaseType().IsArray()
	if arrays {
		usage.Type = usage.Type.BaseType()
		arg, err = b.coerceConditional(operand, usage, raw.Name)
		if err != nil {
			return nil, sqltype.Usage{}, false, err
		}
	} else {
		view := CatalogRead{owner: b.engine, state: b.engine.state}
		if b.queryAuth != nil {
			view = b.queryAuth.catalog
		}
		array, ok := view.LookupArray(usage.Type)
		if !ok || !array.Concrete() {
			return nil, sqltype.Usage{}, false, sqlError("42704", "could not find array type for data type "+b.diagnosticTypeName(usage.Type))
		}
		usage.Type = array
	}
	return arg, usage, arrays, nil
}
