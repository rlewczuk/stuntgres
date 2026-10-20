package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Text-search captures reuse the existing occurrence-keyed analysis cell.
func (b *expressionBinder) regTSConstant(text string, usage sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{offset: pos.Offset, operator: usage.Type.ID()}
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.offset == pos.Offset && c.operator == usage.Type.ID() && c.value.Type() != nil {
				value, err := c.value.Retag(usage.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, dependency := range c.operands {
					b.dependency(dependency.Ref, dependency.Revision)
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
	resolver := input.RegTS.(capturedRegTSInput)
	resolver.qualified = &constant.qualified
	resolver.resolved = &constant.operands
	input.RegTS = resolver
	value, err := usage.Type.InputWithEnvironment(b.ctx, text, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.value = value
	for _, dependency := range constant.operands {
		b.dependency(dependency.Ref, dependency.Revision)
	}
	b.prepared.regtypeConstants = append(b.prepared.regtypeConstants, constant)
	return value, nil
}
