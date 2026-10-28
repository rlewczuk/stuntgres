package lock

import "stuntgres/internal/engine/ids"

// Read-only queries the engine uses to validate its own admitted grants. Each
// runs one manager critical section, returns owned values and never calls out.

// Holds reports whether the live ticket holds any of modes on resource.
func (m *Manager) Holds(t *Ticket, resource Resource, modes Mode) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.holdsLocked(t, resource, modes)
}

func (m *Manager) holdsLocked(t *Ticket, resource Resource, modes Mode) bool {
	entry := m.entries[resource]
	return m.valid(t) && entry != nil && entry.holders[t].Mode&modes != 0
}

// MissingRowGrant returns the index of the first row the live ticket does not
// hold for update, or -1 when it holds them all. An invalid ticket misses at
// the first row; an empty row set never misses.
func (m *Manager) MissingRowGrant(t *Ticket, ref ids.ObjectRef, rows []ids.RowID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.missingRowGrantLocked(t, ref, rows)
}

func (m *Manager) missingRowGrantLocked(t *Ticket, ref ids.ObjectRef, rows []ids.RowID) int {
	for i, row := range rows {
		if !m.holdsLocked(t, Resource{ObjectRef: ref, Row: row}, ForNoKeyUpdate|ForUpdate) {
			return i
		}
	}
	return -1
}

// ReplacementGrants observes a descendant replacement's target RowExclusive
// grant and its row update grants in one critical section. missing is the
// first row lacking a grant, or -1.
func (m *Manager) ReplacementGrants(t *Ticket, ref ids.ObjectRef, rows []ids.RowID) (target bool, missing int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.holdsLocked(t, Resource{ObjectRef: ref}, RowExclusive), m.missingRowGrantLocked(t, ref, rows)
}

// ValidSession reports whether t is the live persistent ticket of token.
func (m *Manager) ValidSession(t *Ticket, token ids.SessionToken) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return t != nil && t.session == token && t.persistent && m.valid(t)
}

// TransactionTicket returns the registered ticket of a transaction, or nil.
func (m *Manager) TransactionTicket(ref ids.TxRef) *Ticket {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.owners[ref]
}

// Capturing granted holders is not an acquisition of Share: later writers stay
// admissible and cannot be appended to this exact transaction cohort.
func (m *Manager) IndexWriters(ticket *Ticket, parent ids.ObjectRef) ([]*Ticket, error) {
	return m.RelationHolders(ticket, parent, Share)
}

// RelationHolders freezes the current conflicting granted owners without
// acquiring or queuing the requested mode. Later holders never join this cohort.
func (m *Manager) RelationHolders(ticket *Ticket, parent ids.ObjectRef, conflict Mode) ([]*Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(ticket) || ticket.persistent || parent.Domain() != ticket.ref.Domain() || parent.Epoch != ticket.ref.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	var cohort []*Ticket
	if entry := m.entries[Resource{ObjectRef: parent}]; entry != nil {
		for owner, mode := range entry.holders {
			if !sameLockSession(owner, ticket) && m.valid(owner) && Conflicts(conflict, mode.Mode) {
				cohort = append(cohort, owner)
			}
		}
	}
	return cohort, nil
}

// AppendRelationNameClaims snapshots other owners' granted relation-name claims
// in namespace. Generated names avoid these still-private catalog destinations,
// just as they avoid committed names. Pending-only requests reserve no name.
// Call outside engine/session mutexes; returned keys own no manager state.
func (m *Manager) AppendRelationNameClaims(dst []ids.NameKey, ticket *Ticket, namespace ids.ObjectRef) ([]ids.NameKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(ticket) || namespace.Domain() != ticket.ref.Domain() || namespace.Epoch != ticket.ref.Epoch() || namespace.Address.ClassID != ids.NamespaceClassID || namespace.Address.SubID != 0 || namespace.Address.ObjectID == 0 {
		return dst, ids.ErrCatalogReplaced
	}
	for resource, entry := range m.entries {
		if resource.ObjectRef != namespace || resource.Name.ClassID != ids.RelationClassID || resource.Name.Namespace != namespace.Address {
			continue
		}
		for owner, held := range entry.holders {
			if owner != ticket && m.valid(owner) && held.Mode&AccessExclusive != 0 {
				dst = append(dst, resource.Name)
				break
			}
		}
	}
	return dst, nil
}
