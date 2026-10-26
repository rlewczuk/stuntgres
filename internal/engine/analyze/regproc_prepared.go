package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Routine literals share the prepared catalog-identifier analysis lifecycle;
// the alias type distinguishes captures at the same source occurrence.
func (b *Binder) RoutineConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	constant := RegtypeConstant{Offset: pos.Offset, TypeID: usage.Type.ID()}
	for _, c := range b.Expected.Regtypes {
		if c.Offset == pos.Offset && c.TypeID == constant.TypeID && c.Value.Type() != nil {
			value, err := c.Value.Retag(usage.Type)
			if err != nil {
				return sqltype.Value{}, err
			}
			for _, operand := range c.Operands {
				b.captureDependency(operand.Ref, operand.Revision, catalog.OperandDependency)
			}
			b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, c)
			return value, nil
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.Input
	target := IdentifierTarget{Position: pos, Qualified: &constant.Qualified, Operands: &constant.Operands}
	input.Regproc = input.Regproc.(RoutineRetarget).RetargetRoutine(target)
	if types, ok := input.Regtype.(RegtypeRetarget); ok {
		input.Regtype = types.RetargetRegtype(target)
	}
	if operator, ok := input.Operator.(OperatorRetarget); ok {
		input.Operator = operator.RetargetOperator(target)
	}
	value, err := usage.Type.InputWithEnvironment(b.Ctx, text, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, RegtypeInputError(err, pos)
	}
	constant.Value = value
	for _, operand := range constant.Operands {
		b.captureDependency(operand.Ref, operand.Revision, catalog.OperandDependency)
	}
	b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
	return value, nil
}
