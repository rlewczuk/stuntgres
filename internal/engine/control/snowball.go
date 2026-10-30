package control

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Remove defaults before appending the complete current ordered option list.
// Replaying only supplied values would resurrect a removed stock stoplist.
func seedDictionaryOptionDelta(d *catalog.TSDictionary) string {
	defaults := d.SeedOptions()
	if slices.Equal(defaults, d.Options) {
		return ""
	}
	parts := make([]string, 0, len(defaults)+len(d.Options))
	for _, option := range defaults {
		parts = append(parts, catalog.DependencyIdentifier(option.Name))
	}
	for _, option := range d.Options {
		parts = append(parts, catalog.DictionaryOptionsText([]catalog.DictionaryOption{option}))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// Identity replay precedes declaration/mapping replay; use the ordinary
// collision planner so simple/English/Arabic/Armenian/Basque/Danish/Dutch/Estonian name cycles remain real SQL.
func (s *dumpScript) configurationProviderReplays() ([]providerReplay, error) {
	var out []providerReplay
	originalNS := s.CatalogView.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	for _, descriptor := range s.CatalogView.Descriptors() {
		d, ok := descriptor.(*catalog.TSConfiguration)
		if !ok || !d.Seeded() {
			continue
		}
		if err := (catalog.TSConfigurationProvider{}).Validate(s.CatalogView, d); err != nil {
			return nil, err
		}
		ns := s.CatalogView.MustNamespace(d.Namespace())
		p := providerReplay{
			family:       configurationProvider,
			original:     providerKey{originalNS.Name(), d.SeedName(), ""},
			current:      providerKey{ns.Name(), d.Name(), ""},
			ownerChanged: d.Owner != s.CatalogView.Owner().Principals.Principal(authorization.BootstrapPrincipalID),
		}
		if p.ownerChanged {
			owner, err := s.DomainRoles.Descriptor(d.Owner)
			if err != nil {
				return nil, err
			}
			p.owner = owner.Name
		}
		out = append(out, p)
	}
	return out, nil
}
