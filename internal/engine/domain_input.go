package engine

import (
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) decodeLiteralInput(text string, typ *sqltype.Descriptor, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	if !sqltype.ContainsDomainConstraints(typ) {
		return typ.InputWithEnvironment(b.Ctx, text, typmod, input)
	}
	return b.Host.DecodeDomainLiteral(b.Ctx, b.Query.Auth, b.BindingCatalog(), typ, text, typmod, input)
}
