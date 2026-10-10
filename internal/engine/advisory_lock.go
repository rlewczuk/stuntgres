package engine

import (
	"context"
	"errors"
)

// This delegate owns only the physical lifetime handle. Counts, granted modes,
// queues and terminal decisions belong exclusively to relationLockManager.
type sessionAdvisory struct{ ticket *relationTicket }

func (a *sessionAdvisory) EndSession(token SessionToken, _ EndReason) error {
	if a.ticket.session != token {
		return errors.New("stuntgres: foreign advisory session")
	}
	a.ticket.manager.end(a.ticket)
	return nil
}

func (m *relationLockManager) registerSession(token SessionToken) (*relationTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token.owner != m.engine || token.id == 0 {
		return nil, errors.New("stuntgres: invalid advisory session")
	}
	if m.sessions[token] != nil {
		return nil, errors.New("stuntgres: duplicate advisory session")
	}
	if m.sessions == nil {
		m.sessions = make(map[SessionToken]*relationTicket)
	}
	t := &relationTicket{manager: m, session: token, persistent: true}
	m.sessions[token] = t
	return t, nil
}

type lockParticipant struct {
	session  SessionToken
	prepared TxRef
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
	return source != nil && !source.detached && source.session == r.ticket.session && r.provenance.epoch >= m.epoch
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
	var notify *relationRequest
	m.mu.Lock()
	if !m.valid(t) || !t.persistent {
		m.mu.Unlock()
		return errors.New("stuntgres: invalid advisory session")
	}
	for ref := range t.resources {
		if e := m.entries[ref]; e != nil {
			m.removeAdvisoryHold(t, ref, e)
			m.settle(e, &notify)
			m.retire(ref, e)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
	return nil
}

func (s *StatementScope) acquireAdvisory(ctx context.Context, ref lockResource, mode relationLockMode, try bool) (bool, error) {
	e := s.op.session.Engine()
	policy := relationLockPolicy{nowait: try, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext, provenance: s.txRef}
	request, err := e.locks.startAcquire(ctx, s.op.session.advisory.ticket, ref, mode, policy)
	if errors.Is(err, errRelationUnavailable) {
		return false, nil
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
		if err == nil {
			// Retain the original provenance, never retry a replacement name.
			e.replacementMu.RLock()
			s.replacementRead = true
			e.mu.Lock()
			if s.txRef.epoch != e.state.epoch {
				err = ErrCatalogReplaced
			}
			e.mu.Unlock()
		}
	}
	return err == nil, err
}
