package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Text-search captures reuse the existing occurrence-keyed analysis cell.
func (b *Binder) RegTSConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	constant := RegtypeConstant{Offset: pos.Offset, Operator: usage.Type.ID()}
	for _, c := range b.Expected.Regtypes {
		if c.Offset == pos.Offset && c.Operator == usage.Type.ID() && c.Value.Type() != nil {
			value, err := c.Value.Retag(usage.Type)
			if err != nil {
				return sqltype.Value{}, err
			}
			for _, dependency := range c.Operands {
				b.captureDependency(dependency.Ref, dependency.Revision, catalog.OperandDependency)
			}
			b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, c)
			return value, nil
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.Input
	input.RegTS = input.RegTS.(RegTSRetarget).RetargetRegTS(IdentifierTarget{Qualified: &constant.Qualified, Resolved: &constant.Operands})
	value, err := usage.Type.InputWithEnvironment(b.Ctx, text, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, RegtypeInputError(err, pos)
	}
	constant.Value = value
	for _, dependency := range constant.Operands {
		b.captureDependency(dependency.Ref, dependency.Revision, catalog.OperandDependency)
	}
	b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
	return value, nil
}
