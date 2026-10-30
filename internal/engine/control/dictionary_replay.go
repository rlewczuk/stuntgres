package control

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// dictionaryProviderReplays shares the provider collision planner, but never
// shares routine collision keys. All initialized dictionary identities must
// participate: an installed member can occupy a seed's vacated name.
func (s *dumpScript) dictionaryProviderReplays() ([]providerReplay, error) {
	providers := make([]providerReplay, 0, 4)
	add := func(d *catalog.TSDictionary, original providerKey, owner authorization.PrincipalRef) error {
		if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(s.CatalogView, d); err != nil {
			return err
		}
		ns, ok := s.CatalogView.Namespace(d.Namespace())
		if !ok {
			return extension.InvalidProviderDescriptor()
		}
		r := providerReplay{family: dictionaryProvider, original: original, current: providerKey{ns.Name(), d.Name(), ""}, ownerChanged: d.Owner != owner}
		if d.Seeded() {
			r.options = seedDictionaryOptionDelta(d)
		} else {
			if extension.UnaccentDictionaryProviderOf(d) != extension.StockUnaccent {
				return extension.InvalidProviderDescriptor()
			}
		}
		if r.ownerChanged {
			role, err := s.DomainRoles.Descriptor(d.Owner)
			if err != nil {
				return err
			}
			r.owner = role.Name
		}
		providers = append(providers, r)
		return nil
	}
	if s.CatalogView.Owner() == nil || s.CatalogView.Owner().Principals == nil {
		return nil, extension.InvalidProviderDescriptor()
	}
	// Namespace replay runs first, so the initialized dictionary already follows
	// its released namespace identity to that namespace's captured name. Its
	// current namespace may differ if the dictionary itself was moved.
	originalNS, ok := s.CatalogView.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	if !ok {
		return nil, extension.InvalidProviderDescriptor()
	}
	for _, descriptor := range s.CatalogView.Descriptors() {
		dictionary, ok := descriptor.(*catalog.TSDictionary)
		if !ok || !dictionary.Seeded() {
			continue
		}
		if err := add(dictionary, providerKey{originalNS.Name(), dictionary.SeedName(), ""}, s.CatalogView.Owner().Principals.Principal(authorization.BootstrapPrincipalID)); err != nil {
			return nil, err
		}
	}
	return providers, nil
}
