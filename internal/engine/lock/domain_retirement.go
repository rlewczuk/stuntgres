package lock

import "stuntgres/internal/engine/ids"

// RetireDomain is terminal, unlike an epoch fence. The shared role domain and
// issuer's database utility ticket remain alive. Notifications run after mu.
func (m *Manager) RetireDomain(domain *ids.Domain) {
	var notify *relationRequest
	m.mu.Lock()
	state := m.domains[domain]
	if state == nil || domain == m.roleDomain || state.retired {
		m.mu.Unlock()
		return
	}
	state.retired = true
	// Terminalize the complete selected wait cohort before releasing any hold.
	// Otherwise ending a holder could grant a selected waiter which happens to
	// be visited later, publishing success during terminal domain retirement.
	for ref, entry := range m.entries {
		retiringResource := ref.Domain() == domain || ref.Database == domain
		for _, request := range entry.pending {
			if !request.terminal && (retiringResource || request.ticket.session.Domain() == domain || request.provenance.Domain() == domain) {
				terminalRequest(request, ErrOwnerEnded, &notify)
			}
		}
	}
	for ref, owner := range m.owners {
		if ref.Domain() == domain {
			m.endLocked(owner, ErrOwnerEnded, &notify)
		}
	}
	for token, owner := range m.sessions {
		if token.Domain() == domain {
			m.endLocked(owner, ErrOwnerEnded, &notify)
		}
	}
	m.retireMultiXactsLocked(domain)
	state.multiHistory = nil
	for ref, entry := range m.entries {
		if ref.Domain() != domain && ref.Database != domain {
			continue
		}
		for _, request := range entry.pending {
			if !request.terminal {
				terminalRequest(request, ErrOwnerEnded, &notify)
			}
		}
		for owner := range entry.holders {
			delete(owner.resources, ref)
		}
		delete(m.entries, ref)
		if ref.key != nil {
			m.retireUniqueKey(ref.key)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}
