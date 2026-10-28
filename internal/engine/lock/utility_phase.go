package lock

import "stuntgres/internal/engine/ids"

// RetireDetachPhase releases only the first phase's SUE relation grants. The
// autocommit utility owns these grants exclusively; its transaction ticket and
// unrelated admission grants remain alive for cancellation and final completion.
// Capture the original conflict cohort before waking newly admissible lockers.
func (m *Manager) RetireDetachPhase(ticket *Ticket, parent ids.ObjectRef, descendants []ids.ObjectRef) ([]*Ticket, error) {
	var notify *relationRequest
	m.mu.Lock()
	if !m.valid(ticket) || ticket.persistent || parent.Domain() != ticket.ref.Domain() || parent.Epoch != ticket.ref.Epoch() {
		m.mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	for _, ref := range descendants {
		if ref.Domain() != parent.Domain() || ref.Epoch != parent.Epoch {
			m.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
	}
	var cohort []*Ticket
	if entry := m.entries[Resource{ObjectRef: parent}]; entry != nil {
		for owner, held := range entry.holders {
			if !sameLockSession(owner, ticket) && m.valid(owner) && Conflicts(AccessExclusive, held.Mode) {
				cohort = append(cohort, owner)
			}
		}
	}
	for i := -1; i < len(descendants); i++ {
		ref := parent
		if i >= 0 {
			ref = descendants[i]
		}
		resource := Resource{ObjectRef: ref}
		if entry := m.entries[resource]; entry != nil {
			held := entry.holders[ticket]
			held.ordinary &^= ShareUpdateExclusive
			held.Mode = held.ordinary | held.scopedModes()
			if held.Mode == 0 {
				m.removeAdvisoryHold(ticket, resource, entry)
			} else {
				entry.holders[ticket] = held
			}
			m.settle(entry, &notify)
			m.retire(resource, entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
	return cohort, nil
}
