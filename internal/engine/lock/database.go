package lock

import "stuntgres/internal/engine/ids"

// Caller holds manager metadata. Startup has one narrowly admitted ordinary
// resource; immediate/queued admission, inquiry and release must agree on it.
func (m *Manager) databaseAdmissionResource(resource Resource, mode Mode) bool {
	return m.roleDomain != nil && m.roleEpoch != 0 && resource.Kind == OrdinaryResource &&
		resource.Domain() == m.roleDomain && resource.Epoch == m.roleEpoch &&
		resource.Address.ClassID == ids.DatabaseClassID && resource.Address.ObjectID != 0 && resource.Address.SubID == 0 &&
		resource.Database == nil && !resource.RoleTuple && resource.Name == (ids.NameKey{}) &&
		resource.Row == 0 && resource.key == nil && resource.completion == nil &&
		resource.High == 0 && resource.Low == 0 && resource.DefaultClass == 0 && mode == RowExclusive
}

// ReleaseDatabaseAdmission drops only the short RowExclusive startup grant on a genuine
// shared database. It cannot release arbitrary persistent or transaction holds.
func (m *Manager) ReleaseDatabaseAdmission(t *Ticket, resource Resource) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && t.persistent && m.databaseAdmissionResource(resource, RowExclusive) {
		if entry := m.entries[resource]; entry != nil && entry.holders[t].Mode == RowExclusive {
			delete(entry.holders, t)
			delete(t.resources, resource)
			if len(t.resources) == 0 {
				t.resources = nil
			}
			m.settle(entry, &notify)
			m.retire(resource, entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}
