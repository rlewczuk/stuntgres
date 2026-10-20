package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Operator constants share the existing catalog-identifier analysis cell and
// namespace authority validation, with the target alias included in the key.
func (b *expressionBinder) operatorConstant(text string, typ *sqltype.Descriptor, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{Offset: pos.Offset, Operator: typ.ID()}
	found := false
	for _, c := range b.expected.Regtypes {
		if c.Offset == pos.Offset && c.Operator == typ.ID() {
			constant = c
			found = true
			break
		}
	}
	if !found {
		auth, err := b.bindingAuthorization()
		if err != nil {
			return sqltype.Value{}, err
		}
		constant.ID, constant.Qualified, constant.Operands, err = b.host.ResolveOperatorText(b.ctx, auth, pos, typ.ID() == sqltype.Regoper, text)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, operand := range constant.Operands {
		b.dependency(operand.Ref, operand.Revision)
	}
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return sqltype.OperatorValue(typ, constant.ID)
}

func (b *expressionBinder) operatorCompoundConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	for _, constant := range b.expected.Regtypes {
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
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	constant := regtypeConstant{Offset: pos.Offset, Operator: usage.Type.ID()}
	if resolver, ok := input.Operator.(analyze.OperatorRetarget); ok {
		input.Operator = resolver.RetargetOperator(analyze.IdentifierTarget{Position: pos, Qualified: &constant.Qualified, Operands: &constant.Operands})
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
