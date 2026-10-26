package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Operator constants share the existing catalog-identifier analysis cell and
// namespace authority validation, with the target alias included in the key.
func (b *Binder) OperatorConstant(text string, typ *sqltype.Descriptor, pos parser.Position) (sqltype.Value, error) {
	constant := RegtypeConstant{Offset: pos.Offset, Operator: typ.ID()}
	found := false
	for _, c := range b.Expected.Regtypes {
		if c.Offset == pos.Offset && c.Operator == typ.ID() {
			constant = c
			found = true
			break
		}
	}
	if !found {
		auth, err := b.BindingAuthorization()
		if err != nil {
			return sqltype.Value{}, err
		}
		constant.ID, constant.Qualified, constant.Operands, err = b.Host.ResolveOperatorText(b.Ctx, auth, pos, typ.ID() == sqltype.Regoper, text)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, operand := range constant.Operands {
		b.captureDependency(operand.Ref, operand.Revision, catalog.OperandDependency)
	}
	b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
	return sqltype.OperatorValue(typ, constant.ID)
}

func (b *Binder) operatorCompoundConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	for _, constant := range b.Expected.Regtypes {
		if constant.Offset == pos.Offset && constant.Operator == usage.Type.ID() && constant.Value.Type() != nil {
			value, err := constant.Value.Retag(usage.Type)
			if err != nil {
				return sqltype.Value{}, err
			}
			for _, operand := range constant.Operands {
				b.captureDependency(operand.Ref, operand.Revision, catalog.OperandDependency)
			}
			b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
			return value, nil
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.Input
	constant := RegtypeConstant{Offset: pos.Offset, Operator: usage.Type.ID()}
	if resolver, ok := input.Operator.(OperatorRetarget); ok {
		input.Operator = resolver.RetargetOperator(IdentifierTarget{Position: pos, Qualified: &constant.Qualified, Operands: &constant.Operands})
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
