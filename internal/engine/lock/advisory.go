package lock

import (
	"errors"

	"stuntgres/internal/engine/ids"
)

// RegisterSession admits a session's persistent advisory ticket. The engine
// has already checked the token's private-session attachment.
func (m *Manager) RegisterSession(token ids.SessionToken) (*Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.domains[token.Domain()] == nil || token.ID() == 0 && token.Private() == nil {
		return nil, errors.New("stuntgres: invalid advisory session")
	}
	if m.sessions[token] != nil {
		return nil, errors.New("stuntgres: duplicate advisory session")
	}
	if m.sessions == nil {
		m.sessions = make(map[ids.SessionToken]*Ticket)
	}
	t := &Ticket{manager: m, session: token, persistent: true}
	m.sessions[token] = t
	return t, nil
}

type Participant struct {
	Session  ids.SessionToken
	Prepared ids.TxRef
}

func (t *Ticket) participant() Participant {
	if t.detached {
		return Participant{Prepared: t.ref}
	}
	return Participant{Session: t.session}
}

func sameLockSession(a, b *Ticket) bool { return a.participant() == b.participant() }

// PREPARE publication has completed and transaction metadata is released, but
// the originating statement still owns admission. Concurrent completion may
// already have retired the ticket; never resurrect it or fail after publication.
func (m *Manager) DetachPrepared(t *Ticket) {
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		t.detached = true
	}
	m.mu.Unlock()
}

func (m *Manager) validRequest(r *relationRequest) bool {
	if !m.valid(r.ticket) {
		return false
	}
	if !r.ticket.persistent {
		return r.ticket.ref.Epoch() >= m.domains[r.ticket.ref.Domain()].epoch
	}
	source := m.owners[r.provenance]
	return source != nil && !source.detached && source.session == r.ticket.session && r.provenance.Epoch() >= m.domains[source.ref.Domain()].epoch
}

// Caller holds manager metadata. Both immediate and queued grants use this
// exact boundary: count publication precedes terminal notification.
func (m *Manager) grant(t *Ticket, ref Resource, e *relationEntry, mode Mode) error {
	if ref.completion != nil {
		return nil
	}
	held := e.holders[t]
	if t.persistent {
		switch mode {
		case AdvisoryShare:
			if held.Shared == ^uint64(0) {
				return errors.New("stuntgres: advisory hold count exhausted")
			}
			held.Shared++
		case AdvisoryExclusive:
			if held.Exclusive == ^uint64(0) {
				return errors.New("stuntgres: advisory hold count exhausted")
			}
			held.Exclusive++
		}
	} else if held.Mode&mode == mode {
		return nil
	}
	if t.uniqueDeltaActive && !t.persistent && ref.Kind == OrdinaryResource && ref.key != nil && mode&UniqueClaim != 0 && held.Mode&UniqueClaim == 0 {
		t.uniqueDelta = append(t.uniqueDelta, ref)
	}
	held.Mode |= mode
	if t.resources == nil {
		t.resources = make(map[Resource]struct{})
	}
	e.holders[t] = held
	t.resources[ref] = struct{}{}
	return nil
}

func (m *Manager) UnlockAdvisory(t *Ticket, ref Resource, mode Mode) (bool, error) {
	var notify *relationRequest
	m.mu.Lock()
	if !m.valid(t) || !t.persistent || ref.Database != t.session.Domain() || ref.ObjectRef != (ids.ObjectRef{}) || ref.RoleTuple || ref.Name != (ids.NameKey{}) || ref.Row != 0 || ref.key != nil || ref.completion != nil || ref.Kind != AdvisoryBigintResource && ref.Kind != AdvisoryPairResource || mode != AdvisoryShare && mode != AdvisoryExclusive {
		m.mu.Unlock()
		return false, errors.New("stuntgres: invalid advisory unlock")
	}
	e := m.entries[ref]
	if e == nil {
		m.mu.Unlock()
		return false, nil
	}
	held := e.holders[t]
	if mode == AdvisoryShare {
		if held.Shared == 0 {
			m.mu.Unlock()
			return false, nil
		}
		held.Shared--
		if held.Shared == 0 {
			held.Mode &^= AdvisoryShare
		}
	} else {
		if held.Exclusive == 0 {
			m.mu.Unlock()
			return false, nil
		}
		held.Exclusive--
		if held.Exclusive == 0 {
			held.Mode &^= AdvisoryExclusive
		}
	}
	if held.Mode == 0 {
		m.removeAdvisoryHold(t, ref, e)
	} else {
		e.holders[t] = held
	}
	m.settle(e, &notify)
	m.retire(ref, e)
	m.mu.Unlock()
	notifyRequests(notify)
	return true, nil
}

func (m *Manager) removeAdvisoryHold(t *Ticket, ref Resource, e *relationEntry) {
	delete(e.holders, t)
	for _, r := range e.pending {
		if r.ticket == t && !r.terminal {
			return
		}
	}
	delete(t.resources, ref)
}

func (m *Manager) UnlockAdvisoryAll(t *Ticket) error {
	m.mu.Lock()
	if !m.valid(t) || !t.persistent {
		m.mu.Unlock()
		return errors.New("stuntgres: invalid advisory session")
	}
	notify := m.unlockAdvisoryHoldsLocked(t)
	m.mu.Unlock()
	notifyRequests(notify)
	return nil
}

// DISCARD's preflight validates this persistent ticket before publishing any
// catalog effects. A live operation lease prevents terminal ticket removal.
func (m *Manager) DiscardAdvisoryHolds(t *Ticket) {
	m.mu.Lock()
	notify := m.unlockAdvisoryHoldsLocked(t)
	m.mu.Unlock()
	notifyRequests(notify)
}

func (m *Manager) unlockAdvisoryHoldsLocked(t *Ticket) *relationRequest {
	var notify *relationRequest
	for ref := range t.resources {
		if e := m.entries[ref]; e != nil {
			m.removeAdvisoryHold(t, ref, e)
			m.settle(e, &notify)
			m.retire(ref, e)
		}
	}
	return notify
}

// Caller owns the serialized transaction operation, but no transaction,
// session or engine metadata. Only genuine overlapping grants restrict PREPARE.
func (m *Manager) CheckPreparedAdvisory(t *Ticket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(t) || t.persistent || t.detached {
		return ids.ErrCatalogReplaced
	}
	session := m.sessions[t.session]
	for ref := range t.resources {
		if ref.Kind != AdvisoryBigintResource && ref.Kind != AdvisoryPairResource {
			continue
		}
		if entry := m.entries[ref]; entry != nil && entry.holders[t].Mode != 0 && entry.holders[session].Mode != 0 {
			return sqlError("0A000", "cannot PREPARE while holding both session-level and transaction-level locks on the same object")
		}
	}
	return nil
}
