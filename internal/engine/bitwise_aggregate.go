package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindBitwiseAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	if b.aggregateClause == "INSERT VALUES" && b.queryNamespace == nil {
		// As with SUM, destination columns are diagnostic candidates, not inputs.
		b.queryNamespace = &queryNamespace{}
		hidden := b.queryHidden
		if target := b.insertTarget; target != nil {
			columns := make([]queryColumn, len(target.columns))
			for i, descriptor := range target.columns {
				columns[i].descriptor = descriptor
			}
			b.queryHidden = append(b.queryHidden, queryQualifier{name: target.name, columns: columns, lateralForbidden: true})
		}
		defer func() { b.queryNamespace, b.queryHidden = nil, hidden }()
	}
	var operand comparisonOperand
	var unary [1]string
	var types []string
	if len(raw.Args) == 1 {
		types = unary[:]
	} else if len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = o
		}
		if types != nil {
			types[i] = b.diagnosticTypeName(o.descriptor())
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := b.resolveBitwiseAggregateSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	operation := aggregateBitAnd
	if raw.Name == "bit_or" {
		operation = aggregateBitOr
	}
	if raw.Name == "bit_xor" {
		operation = aggregateBitXor
	}
	usage := arg.ResultUsage().Type.Usage()
	operator, err := bitwiseAggregateOperator(operation)
	if err != nil {
		return nil, err
	}
	var provider aggregateProvider
	if usage.Type.ID() == sqltype.Bit {
		provider = functions.NewBitStringAggregateProvider(usage, operator)
	} else {
		bound, err := sqltype.BindBitwise(operator, usage.Type, usage.Type)
		if err != nil {
			return nil, err
		}
		provider = functions.NewIntegralBitwiseAggregateProvider(usage, operator, bound)
	}
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, raw.Name, usage.Type.ID())
	if err != nil {
		return nil, err
	}
	return b.finishAggregate(raw, call, filter, b.info(usage, raw.Pos, ExprProperties{}))
}

// Window signature analysis reuses resolution/coercion, never ordinary providers.
func (b *expressionBinder) resolveBitwiseAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
	typ := operand.descriptor()
	if typ == nil {
		return nil, functionResolutionErrorForTypes(name, raw.Pos, types, true)
	}
	typ = typ.BaseType()
	switch typ.ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Bit:
	case sqltype.VarBit:
		typ = b.typ(sqltype.Bit)
	default:
		return nil, sumSignatureError(name, raw.Pos, types, len(raw.Order))
	}
	return b.coerceConditional(operand, typ.Usage(), raw.Name)
}

func bitwiseAggregateOperator(operation aggregateOperation) (sqltype.BitwiseOperator, error) {
	switch operation {
	case aggregateBitAnd:
		return sqltype.BitwiseAnd, nil
	case aggregateBitOr:
		return sqltype.BitwiseOr, nil
	case aggregateBitXor:
		return sqltype.BitwiseXor, nil
	default:
		return 0, fmt.Errorf("stuntgres: invalid bitwise aggregate operation")
	}
}
