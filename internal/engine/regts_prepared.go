package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Text-search captures reuse the existing occurrence-keyed analysis cell.
func (b *expressionBinder) regTSConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{Offset: pos.Offset, Operator: usage.Type.ID()}
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.Offset == pos.Offset && c.Operator == usage.Type.ID() && c.Value.Type() != nil {
				value, err := c.Value.Retag(usage.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, dependency := range c.Operands {
					b.dependency(dependency.Ref, dependency.Revision)
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
	input.RegTS = input.RegTS.(analyze.RegTSRetarget).RetargetRegTS(analyze.IdentifierTarget{Qualified: &constant.Qualified, Resolved: &constant.Operands})
	value, err := usage.Type.InputWithEnvironment(b.ctx, text, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.Value = value
	for _, dependency := range constant.Operands {
		b.dependency(dependency.Ref, dependency.Revision)
	}
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return value, nil
}
