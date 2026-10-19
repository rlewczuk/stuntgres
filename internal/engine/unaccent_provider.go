package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
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
