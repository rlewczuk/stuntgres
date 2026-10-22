package lock

// HasAccessShare samples this exact relation's preexisting contribution. The
// caller serializes its statement owner across acquisition and release.
func (m *Manager) HasAccessShare(t *Ticket, resource Resource) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[resource]
	return m.valid(t) && entry != nil && entry.holders[t].Mode&AccessShare != 0
}

// ReleaseAddedAccessShare removes only the function-added bit; earlier SELECT
// grants and all other resources/modes retain their normal transaction lifetime.
func (m *Manager) ReleaseAddedAccessShare(t *Ticket, resource Resource, preexisting bool) {
	if preexisting {
		return
	}
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) {
		if entry := m.entries[resource]; entry != nil {
			held := entry.holders[t]
			held.Mode &^= AccessShare
			if held.Mode == 0 {
				delete(entry.holders, t)
				delete(t.resources, resource)
			} else {
				entry.holders[t] = held
			}
			m.settle(entry, &notify)
			m.retire(resource, entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}
