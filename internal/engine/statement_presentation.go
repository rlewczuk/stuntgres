package engine

import (
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// presentationCaches is the statement's output-presentation slot: the reg*
// spellings, conversion defaults and the regex executor, filled on first use
// by a single evaluator owner and discarded with the scope.
type presentationCaches struct {
	spellings          regobj.SpellingCache
	conversionDefaults map[[2]int]conversionDefault
	// Statement execution has a single evaluator owner; this cache never belongs
	// to a held plan and is discarded with the statement scope.
	pgRegex sqltype.PGRegexExecution
}

// presentation returns the statement's presentation slot, allocating it on first use.
func (s *StatementScope) presentation() *presentationCaches {
	if s.presentationSlot == nil {
		s.presentationSlot = new(presentationCaches)
	}
	return s.presentationSlot
}

// presentationOrZero reads the presentation slot; a nil slot reads as zero.
func (s *StatementScope) presentationOrZero() presentationCaches {
	if s.presentationSlot == nil {
		return presentationCaches{}
	}
	return *s.presentationSlot
}
