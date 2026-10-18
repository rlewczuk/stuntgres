package engine

import "stuntgres/internal/engine/functions"

// unaccentProvider retains installed extension authority in engine; its rules
// are the functions package's stock unaccent.rules.
type unaccentProvider struct{ matcher *functions.UnaccentMatcher }

var stockUnaccent = &unaccentProvider{matcher: functions.StockUnaccent}
