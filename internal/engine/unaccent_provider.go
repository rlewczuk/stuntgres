package engine

import (
	_ "embed"
	"stuntgres/internal/engine/functions"
)

// PostgreSQL REL_18_0 contrib/unaccent/unaccent.rules, PostgreSQL License.
// https://github.com/postgres/postgres/blob/REL_18_0/contrib/unaccent/unaccent.rules
// Portions Copyright (c) 1996-2025, PostgreSQL Global Development Group.
// Portions Copyright (c) 1994, Regents of the University of California.
// https://www.postgresql.org/about/licence/
//
//go:embed unaccent.rules
var stockUnaccentRules string

// unaccentProvider retains installed extension authority in engine.
type unaccentProvider struct{ matcher *functions.UnaccentMatcher }

var stockUnaccent = &unaccentProvider{matcher: functions.CompileUnaccent(stockUnaccentRules)}
