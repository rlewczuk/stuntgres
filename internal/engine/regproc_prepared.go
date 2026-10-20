package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Routine literals share the prepared catalog-identifier analysis lifecycle;
// the alias type distinguishes captures at the same source occurrence.
func (b *expressionBinder) routineConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{offset: pos.Offset, typeID: usage.Type.ID()}
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.offset == pos.Offset && c.typeID == constant.typeID && c.value.Type() != nil {
				value, err := c.value.Retag(usage.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, operand := range c.operands {
					b.dependency(operand.Ref, operand.Revision)
				}
				b.prepared.regtypeConstants = append(b.prepared.regtypeConstants, c)
				return value, nil
			}
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	resolver := input.Regproc.(capturedRoutineInput)
	resolver.types.position = pos
	resolver.types.qualified = &constant.qualified
	resolver.operands = &constant.operands
	input.Regproc = resolver
	if types, ok := input.Regtype.(capturedRegtypeInput); ok {
		types.position = pos
		types.qualified = &constant.qualified
		input.Regtype = types
	}
	if operator, ok := input.Operator.(capturedOperatorInput); ok {
		operator.types.position = pos
		operator.qualified = &constant.qualified
		operator.operands = &constant.operands
		input.Operator = operator
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
