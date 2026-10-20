package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Routine literals share the prepared catalog-identifier analysis lifecycle;
// the alias type distinguishes captures at the same source occurrence.
func (b *expressionBinder) routineConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{Offset: pos.Offset, TypeID: usage.Type.ID()}
	for _, c := range b.expected.Regtypes {
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
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	target := analyze.IdentifierTarget{Position: pos, Qualified: &constant.Qualified, Operands: &constant.Operands}
	input.Regproc = input.Regproc.(analyze.RoutineRetarget).RetargetRoutine(target)
	if types, ok := input.Regtype.(analyze.RegtypeRetarget); ok {
		input.Regtype = types.RetargetRegtype(target)
	}
	if operator, ok := input.Operator.(analyze.OperatorRetarget); ok {
		input.Operator = operator.RetargetOperator(target)
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
