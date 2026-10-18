package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
)

// RegisterSession admits a session's persistent advisory ticket. The engine
// has already checked the token's private-session attachment.
func (m *relationLockManager) RegisterSession(token ids.SessionToken) (*relationTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token.Domain() != m.domain || token.ID() == 0 && token.Private() == nil {
		return nil, errors.New("stuntgres: invalid advisory session")
	}
	if m.sessions[token] != nil {
		return nil, errors.New("stuntgres: duplicate advisory session")
	}
	if m.sessions == nil {
		m.sessions = make(map[ids.SessionToken]*relationTicket)
	}
	t := &relationTicket{manager: m, session: token, persistent: true}
	m.sessions[token] = t
	return t, nil
}

type lockParticipant struct {
	Session  ids.SessionToken
	Prepared ids.TxRef
}

func (t *relationTicket) participant() lockParticipant {
	if t.detached {
		return lockParticipant{Prepared: t.ref}
	}
	return lockParticipant{Session: t.session}
}

func sameLockSession(a, b *relationTicket) bool { return a.participant() == b.participant() }

// PREPARE publication has completed and transaction metadata is released, but
// the originating statement still owns admission. Concurrent completion may
// already have retired the ticket; never resurrect it or fail after publication.
func (m *relationLockManager) DetachPrepared(t *relationTicket) {
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		t.detached = true
	}
	m.mu.Unlock()
}

func (m *relationLockManager) validRequest(r *relationRequest) bool {
	if !m.valid(r.ticket) {
		return false
	}
	if !r.ticket.persistent {
		return true
	}
	source := m.owners[r.provenance]
	return source != nil && !source.detached && source.session == r.ticket.session && r.provenance.Epoch() >= m.epoch
}

// Caller holds manager metadata. Both immediate and queued grants use this
// exact boundary: count publication precedes terminal notification.
func (m *relationLockManager) grant(t *relationTicket, ref lockResource, e *relationEntry, mode relationLockMode) error {
	if ref.completion != nil {
		return nil
	}
	held := e.holders[t]
	if t.persistent {
		switch mode {
		case advisoryShared:
			if held.Shared == ^uint64(0) {
				return errors.New("stuntgres: advisory hold count exhausted")
			}
			held.Shared++
		case advisoryExclusive:
			if held.Exclusive == ^uint64(0) {
				return errors.New("stuntgres: advisory hold count exhausted")
			}
			held.Exclusive++
		}
	} else if held.Mode&mode == mode {
		return nil
	}
	if t.uniqueDeltaActive && !t.persistent && ref.Kind == ordinaryResource && ref.key != nil && mode&uniqueClaim != 0 && held.Mode&uniqueClaim == 0 {
		t.uniqueDelta = append(t.uniqueDelta, ref)
	}
	held.Mode |= mode
	if t.resources == nil {
		t.resources = make(map[lockResource]struct{})
	}
	e.holders[t] = held
	t.resources[ref] = struct{}{}
	return nil
}

func (m *relationLockManager) UnlockAdvisory(t *relationTicket, ref lockResource, mode relationLockMode) (bool, error) {
	var notify *relationRequest
	m.mu.Lock()
	if !m.valid(t) || !t.persistent || ref.Kind != advisoryBigintResource && ref.Kind != advisoryPairResource || mode != advisoryShared && mode != advisoryExclusive {
		m.mu.Unlock()
		return false, errors.New("stuntgres: invalid advisory unlock")
	}
	e := m.entries[ref]
	if e == nil {
		m.mu.Unlock()
		return false, nil
	}
	held := e.holders[t]
	if mode == advisoryShared {
		if held.Shared == 0 {
			m.mu.Unlock()
			return false, nil
		}
		held.Shared--
		if held.Shared == 0 {
			held.Mode &^= advisoryShared
		}
	} else {
		if held.Exclusive == 0 {
			m.mu.Unlock()
			return false, nil
		}
		held.Exclusive--
		if held.Exclusive == 0 {
			held.Mode &^= advisoryExclusive
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

func (m *relationLockManager) removeAdvisoryHold(t *relationTicket, ref lockResource, e *relationEntry) {
	delete(e.holders, t)
	for _, r := range e.pending {
		if r.ticket == t && !r.terminal {
			return
		}
	}
	delete(t.resources, ref)
}

func (m *relationLockManager) UnlockAdvisoryAll(t *relationTicket) error {
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
func (m *relationLockManager) DiscardAdvisoryHolds(t *relationTicket) {
	m.mu.Lock()
	notify := m.unlockAdvisoryHoldsLocked(t)
	m.mu.Unlock()
	notifyRequests(notify)
}

func (m *relationLockManager) unlockAdvisoryHoldsLocked(t *relationTicket) *relationRequest {
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
func (m *relationLockManager) CheckPreparedAdvisory(t *relationTicket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(t) || t.persistent || t.detached {
		return ErrCatalogReplaced
	}
	session := m.sessions[t.session]
	for ref := range t.resources {
		if ref.Kind != advisoryBigintResource && ref.Kind != advisoryPairResource {
			continue
		}
		if entry := m.entries[ref]; entry != nil && entry.holders[t].Mode != 0 && entry.holders[session].Mode != 0 {
			return sqlError("0A000", "cannot PREPARE while holding both session-level and transaction-level locks on the same object")
		}
	}
	return nil
}
