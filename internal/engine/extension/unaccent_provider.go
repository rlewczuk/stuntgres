package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// UnaccentProvider retains installed extension authority; its rules
// are the functions package's stock unaccent.rules.
type UnaccentProvider struct {
	control catalog.ExtensionControl
}

// Control implements catalog.ExtensionImplementation.
func (p *UnaccentProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *UnaccentProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}}
}

var StockUnaccent = &UnaccentProvider{control: catalog.ExtensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: new("text search dictionary that removes accents")}}

func (p *UnaccentProvider) validateRecipe(v catalog.Read, d *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid unaccent manifest") }
	if !currentManifest(v, d) {
		return invalid()
	}
	if p != StockUnaccent || len(d.Members) != 6 {
		return invalid()
	}
	classes := [6]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID}
	for i, member := range d.Members {
		if member.ClassID != classes[i] {
			return invalid()
		}
	}
	signatures := [4]catalog.ProcedureSignature{catalog.UnaccentDictionarySignature, catalog.UnaccentTextSignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}
	for i, signature := range signatures {
		proc, ok := v.Procedure(d.Members[i])
		if !ok || proc.Signature != signature || proc.Implementation != p {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	tmpl, ok := v.TSTemplate(d.Members[4])
	if !ok || tmpl.Extension != d.Address() || tmpl.Init != d.Members[2] || tmpl.Lexize != d.Members[3] || UnaccentTemplateProviderOf(tmpl) != p {
		return invalid()
	}
	if err := ValidateProviderHeader(v, tmpl, ids.TSTemplateClassID); err != nil { return err }
	dict, ok := v.TSDictionary(d.Members[5])
	if !ok || dict.Extension != d.Address() || dict.Template != d.Members[4] || UnaccentDictionaryProviderOf(dict) != p {
		return invalid()
	}
	if err := ValidateProviderHeader(v, dict, ids.TSDictionaryClassID); err != nil { return err }
	return nil
}

const UnaccentTemplateContext = "SQL statement \"CREATE TEXT SEARCH TEMPLATE unaccent (\n\tINIT = unaccent_init,\n\tLEXIZE = unaccent_lexize\n)\"\nextension script file \"unaccent--1.1.sql\", near line 26"
