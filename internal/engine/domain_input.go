package engine

import (
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) decodeLiteralInput(text string, typ *sqltype.Descriptor, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	if !sqltype.ContainsDomainConstraints(typ) {
		return typ.InputWithEnvironment(b.ctx, text, typmod, input)
	}
	return b.host.DecodeDomainLiteral(b.ctx, b.query.auth, b.bindingCatalog(), typ, text, typmod, input)
}
