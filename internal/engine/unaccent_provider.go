package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// unaccentProvider retains installed extension authority in engine; its rules
// are the functions package's stock unaccent.rules.
type unaccentProvider struct {
	control catalog.ExtensionControl
	matcher *functions.UnaccentMatcher
}

// Control implements extensionImplementation.
func (p *unaccentProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *unaccentProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}}
}

var stockUnaccent = &unaccentProvider{control: catalog.ExtensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: new("text search dictionary that removes accents")}, matcher: functions.StockUnaccent}

func (p *unaccentProvider) ValidateExtension(v catalog.Read, d *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid unaccent manifest") }
	if p != stockUnaccent || len(d.Members) != 6 {
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
	}
	tmpl, ok := v.TSTemplate(d.Members[4])
	if !ok || tmpl.Init != d.Members[2] || tmpl.Lexize != d.Members[3] || unaccentTemplateProviderOf(tmpl) != p {
		return invalid()
	}
	dict, ok := v.TSDictionary(d.Members[5])
	if !ok || dict.Template != d.Members[4] || unaccentDictionaryProviderOf(dict) != p {
		return invalid()
	}
	return nil
}

const unaccentTemplateContext = "SQL statement \"CREATE TEXT SEARCH TEMPLATE unaccent (\n\tINIT = unaccent_init,\n\tLEXIZE = unaccent_lexize\n)\"\nextension script file \"unaccent--1.1.sql\", near line 26"
