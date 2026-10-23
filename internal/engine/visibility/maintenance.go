package visibility

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// VacuumCutoffLocked is the freezing cutoff: the next canonical full XID,
// clamped by every assigned full XID whose ticket ownership has not ended.
// Caller holds Engine.mu.
func (reg *Registry) VacuumCutoffLocked() (uint64, error) {
	cutoff, err := NextFullXID(reg.LastAssignedXID())
	if err != nil {
		return 0, err
	}
	for _, entry := range reg.Entries {
		if entry.FullXID != 0 && (entry.Ticket == nil || !entry.Ticket.OwnershipEnded()) && entry.FullXID < cutoff {
			cutoff = entry.FullXID
		}
	}
	return cutoff, nil
}

// VacuumRetirementAllowedLocked reports whether self may retire dead heads: no
// other entry of self's epoch holds a snapshot, a safe builder, a writer XID or
// a live ticket. Caller holds Engine.mu.
func (reg *Registry) VacuumRetirementAllowedLocked(self ids.TxRef) bool {
	for ref, entry := range reg.Entries {
		if ref == self || ref.Epoch() != self.Epoch() {
			continue
		}
		if entry.Snapshot || entry.SafeBuilder || entry.XID != 0 || entry.Ticket != nil && !entry.Ticket.OwnershipEnded() {
			return false
		}
	}
	return true
}

// InitializeMaintenanceLocked seeds store's unknown frozen-XID bound from the
// VACUUM cutoff and its unfrozen rows; a row without a publisher keeps it
// unknown. A nil store or a known bound is left alone. Caller holds
// Engine.mu; it does not allocate.
func (reg *Registry) InitializeMaintenanceLocked(store *storage.Relation) {
	if store == nil || store.Maintenance.FrozenXID != 0 {
		return
	}
	// A transaction can assign its canonical XID before this relation exists,
	// then publish into it later under READ COMMITTED. Empty relations must
	// therefore start no later than the oldest still-active assigned publisher.
	next, err := reg.VacuumCutoffLocked()
	if err != nil {
		return
	}
	for _, row := range store.Versions {
		if row.Frozen {
			continue
		}
		if row.InsertFullXID == 0 {
			return
		}
		if row.InsertFullXID < next {
			next = row.InsertFullXID
		}
	}
	store.Maintenance.FrozenXID = next
}
