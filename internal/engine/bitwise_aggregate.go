package engine

import (
	"context"
	"fmt"
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
		if err != nil { return nil, err }
		if i == 0 { operand = o }
		if types != nil { types[i] = b.diagnosticTypeName(o.descriptor()) }
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil { return nil, err }
	arg, err := b.resolveBitwiseAggregateSignature(raw, name, operand, types)
	if err != nil { return nil, err }
	operation := aggregateBitAnd
	if raw.Name == "bit_or" { operation = aggregateBitOr }
	if raw.Name == "bit_xor" { operation = aggregateBitXor }
	usage := arg.ResultUsage().Type.Usage()
	var provider aggregateProvider
	if usage.Type.ID() == sqltype.Bit {
		provider = bitStringAggregateProvider{usage, operation}
	} else {
		operator, err := bitwiseAggregateOperator(operation)
		if err != nil { return nil, err }
		bound, err := sqltype.BindBitwise(operator, usage.Type, usage.Type)
		if err != nil { return nil, err }
		provider = integralBitwiseAggregateProvider{usage, operation, bound}
	}
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	return b.finishAggregate(raw, call, filter, b.info(usage, raw.Pos, ExprProperties{}))
}

// Window signature analysis reuses resolution/coercion, never ordinary providers.
func (b *expressionBinder) resolveBitwiseAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil { return nil, err }
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
	case aggregateBitAnd: return sqltype.BitwiseAnd, nil
	case aggregateBitOr: return sqltype.BitwiseOr, nil
	case aggregateBitXor: return sqltype.BitwiseXor, nil
	default: return 0, fmt.Errorf("stuntgres: invalid bitwise aggregate operation")
	}
}

type integralBitwiseAggregateProvider struct {
	usage sqltype.Usage
	operation aggregateOperation
	bound sqltype.Bitwise
}

type integralBitwiseAggregateState struct {
	seen bool
	value sqltype.Value
}

func (p integralBitwiseAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p integralBitwiseAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	return &integralBitwiseAggregateState{value: sqltype.Null(p.usage.Type)}, nil
}
func (p integralBitwiseAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil { return err }
	s, ok := state.(*integralBitwiseAggregateState)
	if !ok || s == nil || s.value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid integral bitwise aggregate state")
	}
	if value.Type() != p.usage.Type || value.IsNull() {
		return fmt.Errorf("stuntgres: invalid integral bitwise aggregate datum")
	}
	if !s.seen {
		s.seen, s.value = true, value
	} else {
		v, err := p.bound.Binary(ctx, s.value, value)
		if err != nil { return err }
		s.value = v
	}
	return ctx.Err()
}
func (p integralBitwiseAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	s, ok := state.(*integralBitwiseAggregateState)
	if !ok || s == nil || s.value.Type() != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid integral bitwise aggregate state")
	}
	return s.value, nil
}

type bitStringAggregateProvider struct {
	usage sqltype.Usage
	operation aggregateOperation
}

type bitStringAggregateState struct {
	seen bool
	bits []byte
}

func (p bitStringAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p bitStringAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	return &bitStringAggregateState{}, nil
}
func (p bitStringAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil { return err }
	s, ok := state.(*bitStringAggregateState)
	if !ok || s == nil { return fmt.Errorf("stuntgres: invalid bit string aggregate state") }
	bits, err := value.Bits()
	if err != nil || value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid bit string aggregate datum")
	}
	if !s.seen {
		s.bits = []byte(bits)
		s.seen = true
		return ctx.Err()
	}
	if len(s.bits) != len(bits) {
		operation := "AND"
		if p.operation == aggregateBitOr { operation = "OR" }
		if p.operation == aggregateBitXor { operation = "XOR" }
		return sqlError("22026", "cannot "+operation+" bit strings of different sizes")
	}
	return p.fold(ctx, s, bits)
}

func (p bitStringAggregateProvider) fold(ctx context.Context, s *bitStringAggregateState, bits string) error {
	// Scratch is group-owned. Poll within each actual fold, including absorbing
	// AND/OR states; neither value evaluation nor width checking is short-circuited.
	switch p.operation {
	case aggregateBitAnd:
		for i := range len(bits) {
			if i&4095 == 0 { if err := ctx.Err(); err != nil { return err } }
			if bits[i] == '0' { s.bits[i] = '0' }
		}
	case aggregateBitOr:
		for i := range len(bits) {
			if i&4095 == 0 { if err := ctx.Err(); err != nil { return err } }
			if bits[i] == '1' { s.bits[i] = '1' }
		}
	case aggregateBitXor:
		for i := range len(bits) {
			if i&4095 == 0 { if err := ctx.Err(); err != nil { return err } }
			if s.bits[i] == bits[i] { s.bits[i] = '0' } else { s.bits[i] = '1' }
		}
	default:
		return fmt.Errorf("stuntgres: invalid bit string aggregate operation")
	}
	return ctx.Err()
}
func (p bitStringAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	s, ok := state.(*bitStringAggregateState)
	if !ok || s == nil { return sqltype.Value{}, fmt.Errorf("stuntgres: invalid bit string aggregate state") }
	if !s.seen { return sqltype.Null(p.usage.Type), nil }
	// Safe conversion seals the result: later transitions cannot mutate it.
	value, err := sqltype.BitString(p.usage.Type, string(s.bits))
	if err != nil { return sqltype.Value{}, err }
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	return value, nil
}
