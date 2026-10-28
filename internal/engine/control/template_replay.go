package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// Ownerless templates share the collision planner, but no routine/dictionary
// key space or ownership directives. Installed slot identity survives relabeling.
func (s *dumpScript) templateProviderReplays() ([]providerReplay, error) {
	var providers []providerReplay
	add := func(d *catalog.TSTemplate, original providerKey) error {
		if err := (extension.CatalogProvider{Class:ids.TSTemplateClassID}).Validate(s.CatalogView, d); err != nil { return err }
		ns, ok := s.CatalogView.Namespace(d.Namespace())
		if !ok { return extension.InvalidProviderDescriptor() }
		providers = append(providers, providerReplay{family:templateProvider, original:original, current:providerKey{ns.Name(),d.Name(),""}})
		return nil
	}
	seed, ok := s.CatalogView.TSTemplate(ids.ObjectAddress{ClassID:ids.TSTemplateClassID,ObjectID:3727})
	if !ok { return nil, extension.InvalidProviderDescriptor() }
	ns, ok := s.CatalogView.Namespace(ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PGCatalogNamespaceOID})
	if !ok { return nil, extension.InvalidProviderDescriptor() }
	if err := add(seed, providerKey{ns.Name(),"simple",""}); err != nil { return nil, err }
	for _, event := range s.eventOrder {
		if event.Phase != dump.Declaration { continue }
		ext, ok := s.CatalogView.Extension(ids.ObjectAddress{ClassID:ids.ExtensionClassID,ObjectID:event.OID})
		if !ok { continue }
		for _, address := range ext.Members {
			d, ok := s.CatalogView.TSTemplate(address)
			if !ok { continue }
			if ext.Name() != "unaccent" { return nil, extension.InvalidProviderDescriptor() }
			ns, ok := s.CatalogView.Namespace(ext.TargetNamespace)
			if !ok { return nil, extension.InvalidProviderDescriptor() }
			if err := add(d, providerKey{ns.Name(),"unaccent",""}); err != nil { return nil, err }
		}
	}
	return providers, nil
}
