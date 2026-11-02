package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// DictXsynProvider authenticates the released dict_xsyn declarations. Its Go
// dictionary body needs no server file, shared-library loader or live handle.
type DictXsynProvider struct {
	control catalog.ExtensionControl
}

var StockDictXsyn = &DictXsynProvider{control: catalog.ExtensionControl{Version: "1.0", Trusted: false, Superuser: true, Relocatable: true, Comment: new("text search dictionary template for extended synonym processing")}}

func (p *DictXsynProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *DictXsynProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func (p *DictXsynProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *DictXsynProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, d, p)
}

func (p *DictXsynProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockDictXsyn || !currentManifest(v, e) || e.Name() != "dict_xsyn" || e.Version != "1.0" || len(e.Members) != 4 {
		return InvalidProviderDescriptor()
	}
	for i, class := range [4]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		if e.Members[i].ClassID != class {
			return InvalidProviderDescriptor()
		}
	}
	for _, address := range e.Members[:2] {
		d, ok := v.Procedure(address)
		if !ok {
			return InvalidProviderDescriptor()
		}
		if err := p.ValidateProcedure(v, d); err != nil {
			return err
		}
	}
	template, ok := v.TSTemplate(e.Members[2])
	if !ok || template.Extension != e.Address() || template.Init != e.Members[0] || template.Lexize != e.Members[1] || DictXsynTemplateProviderOf(template) != p {
		return InvalidProviderDescriptor()
	}
	if err := (CatalogProvider{Class: ids.TSTemplateClassID}).Validate(v, template); err != nil {
		return err
	}
	dictionary, ok := v.TSDictionary(e.Members[3])
	if !ok || dictionary.Extension != e.Address() || dictionary.Template != template.Address() || DictXsynDictionaryProviderOf(dictionary) != p || len(dictionary.Options) != 0 {
		return InvalidProviderDescriptor()
	}
	return (CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v, dictionary)
}
