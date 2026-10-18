package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
)

// registerSession admits a session's persistent advisory ticket. The engine
// has already checked the token's private-session attachment.
func (m *relationLockManager) registerSession(token ids.SessionToken) (*relationTicket, error) {
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
	session  ids.SessionToken
	prepared ids.TxRef
}

func (t *relationTicket) participant() lockParticipant {
	if t.detached {
		return lockParticipant{prepared: t.ref}
	}
	return lockParticipant{session: t.session}
}

func sameLockSession(a, b *relationTicket) bool { return a.participant() == b.participant() }

// PREPARE publication has completed and transaction metadata is released, but
// the originating statement still owns admission. Concurrent completion may
// already have retired the ticket; never resurrect it or fail after publication.
func (m *relationLockManager) detachPrepared(t *relationTicket) {
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
			if held.shared == ^uint64(0) {
				return errors.New("stuntgres: advisory hold count exhausted")
			}
			held.shared++
		case advisoryExclusive:
			if held.exclusive == ^uint64(0) {
				return errors.New("stuntgres: advisory hold count exhausted")
			}
			held.exclusive++
		}
	} else if held.mode&mode == mode {
		return nil
	}
	if t.uniqueDeltaActive && !t.persistent && ref.kind == ordinaryResource && ref.key != nil && mode&uniqueClaim != 0 && held.mode&uniqueClaim == 0 {
		t.uniqueDelta = append(t.uniqueDelta, ref)
	}
	held.mode |= mode
	if t.resources == nil {
		t.resources = make(map[lockResource]struct{})
	}
	e.holders[t] = held
	t.resources[ref] = struct{}{}
	return nil
}

func (m *relationLockManager) unlockAdvisory(t *relationTicket, ref lockResource, mode relationLockMode) (bool, error) {
	var notify *relationRequest
	m.mu.Lock()
	if !m.valid(t) || !t.persistent || ref.kind != advisoryBigintResource && ref.kind != advisoryPairResource || mode != advisoryShared && mode != advisoryExclusive {
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
		if held.shared == 0 {
			m.mu.Unlock()
			return false, nil
		}
		held.shared--
		if held.shared == 0 {
			held.mode &^= advisoryShared
		}
	} else {
		if held.exclusive == 0 {
			m.mu.Unlock()
			return false, nil
		}
		held.exclusive--
		if held.exclusive == 0 {
			held.mode &^= advisoryExclusive
		}
	}
	if held.mode == 0 {
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

func (m *relationLockManager) unlockAdvisoryAll(t *relationTicket) error {
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
func (m *relationLockManager) discardAdvisoryHolds(t *relationTicket) {
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
func (m *relationLockManager) checkPreparedAdvisory(t *relationTicket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(t) || t.persistent || t.detached {
		return ErrCatalogReplaced
	}
	session := m.sessions[t.session]
	for ref := range t.resources {
		if ref.kind != advisoryBigintResource && ref.kind != advisoryPairResource {
			continue
		}
		if entry := m.entries[ref]; entry != nil && entry.holders[t].mode != 0 && entry.holders[session].mode != 0 {
			return sqlError("0A000", "cannot PREPARE while holding both session-level and transaction-level locks on the same object")
		}
	}
	return nil
}
