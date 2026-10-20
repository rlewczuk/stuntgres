package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Operator constants share the existing catalog-identifier analysis cell and
// namespace authority validation, with the target alias included in the key.
func (b *expressionBinder) operatorConstant(text string, typ *sqltype.Descriptor, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{offset: pos.Offset, operator: typ.ID()}
	found := false
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.offset == pos.Offset && c.operator == typ.ID() {
				constant = c
				found = true
				break
			}
		}
	}
	if !found {
		auth, err := b.bindingAuthorization()
		if err != nil {
			return sqltype.Value{}, err
		}
		input, err := b.engine.captureOperatorInput(auth, b.op)
		if err != nil {
			return sqltype.Value{}, err
		}
		input.types.position = pos
		input.qualified = &constant.qualified
		input.operands = &constant.operands
		if typ.ID() == sqltype.Regoper {
			constant.id, err = input.ResolveRegoper(b.ctx, text)
		} else {
			constant.id, err = input.ResolveRegoperator(b.ctx, text)
		}
		if err != nil {
			return sqltype.Value{}, regtypeInputError(err, pos)
		}
	}
	for _, operand := range constant.operands {
		b.dependency(operand.Ref, operand.Revision)
	}
	b.prepared.regtypeConstants = append(b.prepared.regtypeConstants, constant)
	return sqltype.OperatorValue(typ, constant.id)
}

func (b *expressionBinder) operatorCompoundConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if b.expected != nil && b.expected.regtypes != nil {
		for _, constant := range b.expected.regtypes.constants {
			if constant.offset == pos.Offset && constant.operator == usage.Type.ID() && constant.value.Type() != nil {
				value, err := constant.value.Retag(usage.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, operand := range constant.operands {
					b.dependency(operand.Ref, operand.Revision)
				}
				b.prepared.regtypeConstants = append(b.prepared.regtypeConstants, constant)
				return value, nil
			}
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	constant := regtypeConstant{offset: pos.Offset, operator: usage.Type.ID()}
	if resolver, ok := input.Operator.(capturedOperatorInput); ok {
		resolver.types.position = pos
		resolver.qualified = &constant.qualified
		resolver.operands = &constant.operands
		input.Operator = resolver
	}
	value, err := usage.Type.InputWithEnvironment(b.ctx, text, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.value = value
	for _, operand := range constant.operands {
		b.dependency(operand.Ref, operand.Revision)
	}
	b.prepared.regtypeConstants = append(b.prepared.regtypeConstants, constant)
	return value, nil
}
