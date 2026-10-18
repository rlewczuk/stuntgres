package engine

import "stuntgres/internal/engine/functions"

// unaccentProvider retains installed extension authority in engine; its rules
// are the functions package's stock unaccent.rules.
type unaccentProvider struct {
	control extensionControl
	matcher *functions.UnaccentMatcher
}

// Control implements extensionImplementation.
func (p *unaccentProvider) Control() extensionControl { return p.control.Detached() }

var stockUnaccent = &unaccentProvider{control: extensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: new("text search dictionary that removes accents")}, matcher: functions.StockUnaccent}
