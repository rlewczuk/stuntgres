package lock

import (
	"context"

	"stuntgres/internal/engine/ids"
)

// TupleMaintenance is caller-owned detached scratch for one exact physical
// identity. Present/Xmax/Multi include raw ended or aborted ownership even when
// Pgrowlocks filters the tuple. No owner authority or membership backing escapes.
type TupleMaintenance struct {
	Tuple   MultiTuple
	Present bool
	Xmax    uint32
	Multi   bool
	// Active is effective ownership, distinct from retained raw presentation.
	Active bool
	// NewestFullXID comes from actual member owners, never the raw Xmax ID.
	// Zero conservatively includes any unknown raw-member provenance.
	NewestFullXID uint64
}

// CaptureTupleMaintenance runs outside Engine/owner mutexes and takes only
// Manager.mu. It never assigns identity, settles queues, touches contribution
// ownership, or changes raw history. The caller discards scratch on failure.
func (m *Manager) CaptureTupleMaintenance(ctx context.Context, caller *Ticket, tuples []TupleMaintenance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(caller) || caller.OwnershipEnded() {
		return ids.ErrCatalogReplaced
	}
	for i := range tuples {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := tuples[i].Tuple
		if !m.validMultiTuple(caller, key) {
			return ids.ErrCatalogReplaced
		}
		row, xmax, multi, present := m.rawTupleHeader(key)
		tuples[i].Xmax, tuples[i].Multi, tuples[i].Present = xmax, multi, present
		tuples[i].Active, tuples[i].NewestFullXID = false, 0
		unknown := false
		for _, member := range row.members {
			if member.owner == nil {
				unknown = true
				continue
			}
			fullXID := member.owner.AssignedFullXID()
			unknown = unknown || fullXID == 0
			tuples[i].NewestFullXID = max(tuples[i].NewestFullXID, fullXID)
			if !member.owner.OwnershipEnded() && member.owner.CompletionState() != ids.TxAborted && member.owner.multiContributions[key]&(1<<member.Mode) != 0 {
				tuples[i].Active = true
			}
		}
		if unknown {
			tuples[i].NewestFullXID = 0
		}
	}
	return nil
}

// rawTupleHeader reads immutable physical history under Manager.mu. Eligibility
// and current owner contributions are deliberately not part of this raw header.
// The returned row is manager-private; public captures detach its scalar facts.
func (m *Manager) rawTupleHeader(key MultiTuple) (multiRow, uint32, bool, bool) {
	row := m.multiRows[key]
	if len(row.members) == 0 {
		return row, 0, false, false
	}
	if row.id != 0 {
		return row, row.id, true, true
	}
	return row, row.members[0].XID, false, true
}
