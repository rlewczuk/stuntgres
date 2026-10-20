package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Routine literals share the prepared catalog-identifier analysis lifecycle;
// the alias type distinguishes captures at the same source occurrence.
func (b *expressionBinder) routineConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{Offset: pos.Offset, TypeID: usage.Type.ID()}
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.Offset == pos.Offset && c.TypeID == constant.TypeID && c.Value.Type() != nil {
				value, err := c.Value.Retag(usage.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, operand := range c.Operands {
					b.dependency(operand.Ref, operand.Revision)
				}
				b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, c)
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
	resolver.types.qualified = &constant.Qualified
	resolver.operands = &constant.Operands
	input.Regproc = resolver
	if types, ok := input.Regtype.(capturedRegtypeInput); ok {
		types.position = pos
		types.qualified = &constant.Qualified
		input.Regtype = types
	}
	if operator, ok := input.Operator.(capturedOperatorInput); ok {
		operator.types.position = pos
		operator.qualified = &constant.Qualified
		operator.operands = &constant.Operands
		input.Operator = operator
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
