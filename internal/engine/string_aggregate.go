package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindStringAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var small [2]comparisonOperand
	var names [2]string
	var operands []comparisonOperand
	var types []string
	if len(raw.Args) <= 2 {
		operands = small[:len(raw.Args)]
		types = names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
			types[i] = "unknown"
			if typ := o.descriptor(); typ != nil {
				types[i] = typ.DiagnosticName()
			}
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	value, separator, err := b.resolveStringAggregateSignature(raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	properties := mergeExpressionProperties(value.Properties(), separator.Properties())
	properties.StageDependent = true
	info, err := b.collatedInfo(b.info(value.ResultUsage().Type.Usage(), raw.Pos, properties), value, separator)
	if err != nil {
		return nil, err
	}
	call := aggregateCall{provider: functions.NewStringAggregateProvider(info.usage), argument: value, separator: separator, operation: aggregateString}
	formal := value.ResultUsage().Type.ID()
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, "string_agg", formal, formal)
	if err != nil {
		return nil, err
	}
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) resolveStringAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operands []comparisonOperand, types []string) (BoundExpr, BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, nil, err
	}
	if !visible || raw.Star || len(operands) != 2 {
		return nil, nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	typ := b.typ(sqltype.Text)
	for _, o := range operands {
		if d := o.descriptor(); d != nil && d.BaseType().ID() == sqltype.Bytea {
			typ = b.typ(sqltype.Bytea)
		}
	}
	for _, o := range operands {
		if d := o.descriptor(); d != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), typ.Usage(), sqltype.Implicit); err != nil {
				return nil, nil, functionSignatureErrorForTypes(name, raw.Pos, types)
			}
		}
	}
	value, err := b.coerceConditional(operands[0], typ.Usage(), raw.Name)
	if err != nil {
		return nil, nil, err
	}
	separator, err := b.coerceConditional(operands[1], typ.Usage(), raw.Name)
	return value, separator, err
}
