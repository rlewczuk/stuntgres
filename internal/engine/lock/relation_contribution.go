package lock

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
)

// RelationContribution is one invocation's grant, not an ordinary transaction
// grant. Pass a fresh zero value to StartAcquireRelation and release it after
// the returned request (if any) is terminal. Do not copy or reuse it.
type RelationContribution struct {
	ticket   *Ticket
	resource Resource
	entry    *relationEntry
	mode     Mode
	started  bool
	granted  bool
}

func relationModeSlot(mode Mode) int {
	if mode == Share {
		return 1
	}
	return 0
}

func (h Hold) scopedModes() Mode {
	var mode Mode
	if h.scoped[0] != 0 {
		mode |= AccessShare
	}
	if h.scoped[1] != 0 {
		mode |= Share
	}
	return mode
}

// StartAcquireRelation uses the ordinary queue and grant publication boundary,
// but records scoped ownership independently of idempotent transaction grants.
func (m *Manager) StartAcquireRelation(ctx context.Context, t *Ticket, resource Resource, mode Mode, policy Policy, contribution *RelationContribution) (*Request, error) {
	if contribution == nil || resource.Kind != OrdinaryResource || resource.Address.ClassID != ids.RelationClassID || resource.Row != 0 || resource.key != nil || resource.Name != (ids.NameKey{}) || resource.completion != nil || mode != AccessShare && mode != Share {
		return nil, errors.New("stuntgres: invalid scoped relation request")
	}
	return m.startAcquire(ctx, t, resource, mode, policy, contribution)
}

// HasRelationMode observes the union used by conflict arbitration and pg_locks.
func (m *Manager) HasRelationMode(t *Ticket, resource Resource, mode Mode) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[resource]
	return m.valid(t) && entry != nil && entry.holders[t].Mode&mode != 0
}

// ReleaseRelation removes exactly this invocation's contribution. Ordinary
// acquisitions before or during the invocation, and other scopes, survive.
func (m *Manager) ReleaseRelation(contribution *RelationContribution) {
	var notify *relationRequest
	m.mu.Lock()
	if contribution != nil && contribution.granted {
		contribution.granted = false
		t, resource := contribution.ticket, contribution.resource
		if m.valid(t) {
			if entry := m.entries[resource]; entry != nil && entry == contribution.entry {
				held := entry.holders[t]
				held.scoped[relationModeSlot(contribution.mode)]--
				held.Mode = held.ordinary | held.scopedModes()
				if held.Mode == 0 {
					m.removeAdvisoryHold(t, resource, entry)
				} else {
					entry.holders[t] = held
				}
				m.settle(entry, &notify)
				m.retire(resource, entry)
			}
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}
