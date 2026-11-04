package analyze

import (
	"stuntgres/internal/sqltype"
)

func (b *Binder) decodeLiteralInput(text string, typ *sqltype.Descriptor, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	// An actual client binder owns Engine.Mu, so it must use the existing
	// unlock/decode/readmission seam before entering a dynamic input callback.
	// A private snapshot constructor instead carries its explicit, already
	// captured TypeIO authority; it never borrows a client session.
	if owner := b.BindingCatalog().Owner(); owner != nil && owner.Sepgsql != nil && input.TypeIO == nil && sqltype.ContainsInstalledScalarIO(typ) {
		return b.Host.DecodeDomainLiteral(b.Ctx, b.Query.Auth, b.BindingCatalog(), typ, text, typmod, input)
	}
	if !sqltype.ContainsDomainConstraints(typ) && (!sqltype.ContainsISNInput(typ) || input.LockedDomainInput == nil) {
		return typ.InputWithEnvironment(b.Ctx, text, typmod, input)
	}
	return b.Host.DecodeDomainLiteral(b.Ctx, b.Query.Auth, b.BindingCatalog(), typ, text, typmod, input)
}
