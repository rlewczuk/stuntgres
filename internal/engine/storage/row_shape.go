package storage

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// RowShape is a catalog stage's planned heap replacement for one relation:
// the observed predecessor, its row root and the private replacement store.
// Publication fences on the observation and installs Replacement.
type RowShape struct {
	Original       *Relation
	Root           []RowVersion
	Replacement    *Relation
	ObservedLength int
	ObservedEnds   *DeletionRoot
	Generation     uint64
	// Typed evolution has already transformed its own rows alongside the heap.
	OwnProjected bool
	Rewritten    bool
	Frontier     ids.CommitSeq
	Fills        map[int32]sqltype.Value
}

// Matches reports whether current is still the observed predecessor: the same
// store, generation and row count. It ignores deletion ends, which change
// under concurrent deletes before a stage validates.
func (s RowShape) Matches(current *Relation) bool {
	return current == s.Original && s.Original.Generation == s.Generation && len(s.Original.Versions) == s.ObservedLength
}

// MatchesWithEnds is Matches plus the observed deletion ends: the
// publication fence, where any delete since the observation is a conflict.
func (s RowShape) MatchesWithEnds(current *Relation) bool {
	return s.Matches(current) && s.Original.Ends == s.ObservedEnds
}
