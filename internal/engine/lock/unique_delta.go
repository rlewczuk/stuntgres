package lock

// A conflict UPDATE candidate journals only newly granted uniqueClaim bits.
// The ticket's single admitted statement owns the mark; grant records both
// immediate and queued outcomes under the existing manager mutex.
func (m *Manager) BeginUniqueDelta(t *Ticket) {
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		t.uniqueDelta = t.uniqueDelta[:0]
		t.uniqueDeltaActive = true
	}
	m.mu.Unlock()
}

func (m *Manager) EndUniqueDelta(t *Ticket) {
	m.mu.Lock()
	t.uniqueDeltaActive = false
	t.uniqueDelta = t.uniqueDelta[:0]
	m.mu.Unlock()
}

// Restoration cannot resurrect an invalid ticket or a released contribution.
// Row/relation/completion/advisory grants and previously held uniqueClaim bits
// never enter this journal. Other bits and counted contributions survive.
func (m *Manager) RestoreUniqueDelta(t *Ticket) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		for _, ref := range t.uniqueDelta {
			entry := m.entries[ref]
			if entry == nil {
				continue
			}
			hold := entry.holders[t]
			if hold.Mode&UniqueClaim == 0 {
				continue
			}
			hold.Mode &^= UniqueClaim
			if hold.Mode == 0 && hold.Shared == 0 && hold.Exclusive == 0 {
				delete(entry.holders, t)
				delete(t.resources, ref)
			} else {
				entry.holders[t] = hold
			}
			m.settle(entry, &notify)
			m.retire(ref, entry)
		}
	}
	t.uniqueDelta = t.uniqueDelta[:0]
	m.mu.Unlock()
	notifyRequests(notify)
}
