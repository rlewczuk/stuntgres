package engine

import (
	"stuntgres/internal/engine/visibility"
)

// partitions returns the statement's partitions slot, allocating it on first use.
func (s *StatementScope) partitions() *partitionScope {
	if s.partitionsSlot == nil {
		s.partitionsSlot = new(partitionScope)
	}
	return s.partitionsSlot
}

// partitionsOrZero reads the partitions slot; a nil slot reads as zero.
func (s *StatementScope) partitionsOrZero() partitionScope {
	if s.partitionsSlot == nil {
		return partitionScope{}
	}
	return *s.partitionsSlot
}

// setPartitionValidationView installs a saved scan view; restoring none
// never allocates the slot.
func (s *StatementScope) setPartitionValidationView(view *visibility.View) {
	if view != nil || s.partitionsSlot != nil {
		s.partitions().ValidationView = view
	}
}
