package engine

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/ids"
)

// blockingPid projects a blocking participant to its public PID: zero for a
// prepared transaction. Foreign, private and unnumbered sessions are not
// visible.
func blockingPid(token ids.SessionToken, prepared bool, domain *ids.Domain) (int32, bool, error) {
	if prepared {
		return 0, true, nil
	}
	if token.Domain() != domain || token.Private() != nil || token.ID() == 0 {
		return 0, false, nil
	}
	if token.ID() > math.MaxInt32 {
		return 0, false, fmt.Errorf("stuntgres: blocking session identity exceeds integer range")
	}
	return int32(token.ID()), true, nil
}

func (m *relationLockManager) validInquiryRequest(r *relationRequest, entry *relationEntry, resource lockResource) bool {
	if r == nil || r.terminal || r.entry != entry || r.ref != resource || !m.validRequest(r) {
		return false
	}
	if _, journaled := r.ticket.resources[resource]; !journaled {
		return false
	}
	if r.ticket.persistent {
		_, journaled := m.owners[r.provenance].resources[resource]
		return journaled
	}
	return true
}

func (m *relationLockManager) pendingInquiryRequest(t *relationTicket) *relationRequest {
	if !m.valid(t) || t.detached {
		return nil
	}
	for resource := range t.resources {
		entry := m.entries[resource]
		if entry == nil {
			continue
		}
		for _, r := range entry.pending {
			if r.ticket == t && m.validInquiryRequest(r, entry, resource) {
				return r
			}
		}
	}
	return nil
}

// Only manager metadata is held. Inspect the authoritative journal and resource
// queue without granting, settling, copying the graph, or invoking callbacks.
// Pids are public session IDs, or zero for a prepared transaction.
func (m *relationLockManager) blockingPids(pid ids.SessionID) ([]int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	token := m.domain.Session(pid)
	r := m.pendingInquiryRequest(m.sessions[token])
	if r == nil {
		for _, t := range m.owners {
			if t.session == token && !t.detached {
				r = m.pendingInquiryRequest(t)
				if r != nil {
					break
				}
			}
		}
	}
	if r == nil {
		return nil, nil
	}
	var values []int32
	// Deduplicate by participant, never by projected PID: prepared owners with
	// different TxRefs must retain separate zero entries. Scanning the small
	// resource-local prefixes avoids allocating a second identity registry.
	appendParticipant := func(t *relationTicket) error {
		pid, visible, err := blockingPid(t.session, t.detached, m.domain)
		if visible {
			values = append(values, pid)
		}
		return err
	}
	for t, held := range r.entry.holders {
		if !m.valid(t) || sameLockSession(t, r.ticket) || !lockConflict(r.mode, held.mode) {
			continue
		}
		// A participant can have session and transaction tickets. Pick one
		// canonical ticket without sorting/copying the holder map.
		duplicate := false
		for other, hold := range r.entry.holders {
			if other == t || !m.valid(other) || !sameLockSession(t, other) || !lockConflict(r.mode, hold.mode) {
				continue
			}
			if other.persistent && !t.persistent || other.persistent == t.persistent && other.ref.ID() < t.ref.ID() {
				duplicate = true
				break
			}
		}
		if !duplicate {
			if err := appendParticipant(t); err != nil {
				return nil, err
			}
		}
	}
	if r.ref.completion != nil || r.ref.row != 0 && r.mode == noKeyUpdate {
		return values, nil
	}
	for i, earlier := range r.entry.pending {
		if earlier == r {
			break
		}
		if !m.validInquiryRequest(earlier, r.entry, r.ref) || sameLockSession(earlier.ticket, r.ticket) || !lockConflict(r.mode, earlier.mode) {
			continue
		}
		duplicate := false
		for t, hold := range r.entry.holders {
			if m.valid(t) && sameLockSession(t, earlier.ticket) && lockConflict(r.mode, hold.mode) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			for _, previous := range r.entry.pending[:i] {
				if m.validInquiryRequest(previous, r.entry, r.ref) && sameLockSession(previous.ticket, earlier.ticket) && lockConflict(r.mode, previous.mode) {
					duplicate = true
					break
				}
			}
		}
		if !duplicate {
			if err := appendParticipant(earlier.ticket); err != nil {
				return nil, err
			}
		}
	}
	return values, nil
}
