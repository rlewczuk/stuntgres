package engine

import "stuntgres/internal/engine/functions"

// unaccentProvider retains installed extension authority in engine; its rules
// are the functions package's stock unaccent.rules.
type unaccentProvider struct {
	control extensionControl
	matcher *functions.UnaccentMatcher
}

// Control implements extensionImplementation.
func (p *unaccentProvider) Control() extensionControl { return p.control.detached() }

var stockUnaccent = &unaccentProvider{control: extensionControl{version: "1.1", trusted: true, superuser: true, relocatable: true, comment: new("text search dictionary that removes accents")}, matcher: functions.StockUnaccent}
