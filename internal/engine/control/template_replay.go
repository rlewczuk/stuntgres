package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// Only the initialized template has a bootstrap identity. Released declarations
// replay with their sealed installation cohort; ordinary templates have CREATE
// events even when they are current extension members.
func (s *dumpScript) templateProviderReplays() ([]providerReplay, error) {
	var providers []providerReplay
	add := func(d *catalog.TSTemplate, original providerKey) error {
		if err := (extension.CatalogProvider{Class: ids.TSTemplateClassID}).Validate(s.CatalogView, d); err != nil {
			return err
		}
		ns, ok := s.CatalogView.Namespace(d.Namespace())
		if !ok {
			return extension.InvalidProviderDescriptor()
		}
		providers = append(providers, providerReplay{family: templateProvider, original: original, current: providerKey{ns.Name(), d.Name(), ""}})
		return nil
	}
	ns, ok := s.CatalogView.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	if !ok {
		return nil, extension.InvalidProviderDescriptor()
	}
	for _, fixed := range []struct {
		oid  ids.OID
		name string
	}{{3727, "simple"}, {3730, "synonym"}} {
		seed, ok := s.CatalogView.TSTemplate(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: fixed.oid})
		if !ok {
			return nil, extension.InvalidProviderDescriptor()
		}
		if err := add(seed, providerKey{ns.Name(), fixed.name, ""}); err != nil {
			return nil, err
		}
	}
	for _, descriptor := range s.CatalogView.Descriptors() {
		d, ok := descriptor.(*catalog.TSTemplate)
		if !ok || !d.Snowball() {
			continue
		}
		if err := add(d, providerKey{ns.Name(), "snowball", ""}); err != nil {
			return nil, err
		}
	}
	return providers, nil
}
