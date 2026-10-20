package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Operator constants share the existing catalog-identifier analysis cell and
// namespace authority validation, with the target alias included in the key.
func (b *expressionBinder) operatorConstant(text string, typ *sqltype.Descriptor, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{Offset: pos.Offset, Operator: typ.ID()}
	found := false
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.Offset == pos.Offset && c.Operator == typ.ID() {
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
		input, err := b.host.CaptureOperatorInput(auth)
		if err != nil {
			return sqltype.Value{}, err
		}
		input.types.position = pos
		input.qualified = &constant.Qualified
		input.operands = &constant.Operands
		if typ.ID() == sqltype.Regoper {
			constant.ID, err = input.ResolveRegoper(b.ctx, text)
		} else {
			constant.ID, err = input.ResolveRegoperator(b.ctx, text)
		}
		if err != nil {
			return sqltype.Value{}, regtypeInputError(err, pos)
		}
	}
	for _, operand := range constant.Operands {
		b.dependency(operand.Ref, operand.Revision)
	}
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return sqltype.OperatorValue(typ, constant.ID)
}

func (b *expressionBinder) operatorCompoundConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if b.expected != nil && b.expected.regtypes != nil {
		for _, constant := range b.expected.regtypes.constants {
			if constant.Offset == pos.Offset && constant.Operator == usage.Type.ID() && constant.Value.Type() != nil {
				value, err := constant.Value.Retag(usage.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, operand := range constant.Operands {
					b.dependency(operand.Ref, operand.Revision)
				}
				b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
				return value, nil
			}
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	constant := regtypeConstant{Offset: pos.Offset, Operator: usage.Type.ID()}
	if resolver, ok := input.Operator.(capturedOperatorInput); ok {
		resolver.types.position = pos
		resolver.qualified = &constant.Qualified
		resolver.operands = &constant.Operands
		input.Operator = resolver
	}
	value, err := usage.Type.InputWithEnvironment(b.ctx, text, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.Value = value
	for _, operand := range constant.Operands {
		b.dependency(operand.Ref, operand.Revision)
	}
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return value, nil
}
