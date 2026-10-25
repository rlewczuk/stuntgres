package core

import (
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/visibility"
)

// partitions returns the statement's partitions slot, allocating it on first use.
func (s *StatementScope) partitions() *partition.Scope {
	if s.partitionsSlot == nil {
		s.partitionsSlot = new(partition.Scope)
	}
	return s.partitionsSlot
}

// PartitionsOrZero reads the partitions slot; a nil slot reads as zero.
func (s *StatementScope) PartitionsOrZero() partition.Scope {
	if s.partitionsSlot == nil {
		return partition.Scope{}
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
