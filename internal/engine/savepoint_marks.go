package engine

import (
	"maps"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Marks retain immutable logical roots and held lock contributions, not
// generator counters or snapshots. The outer block keeps the same ticket.
type savepointMark struct {
	name                string
	serial              uint64 // the owner's savepointSerial at capture; names foreign savepoints
	writes              map[ids.ObjectAddress][]storage.RowVersion
	deletes             map[ids.ObjectAddress]*visibility.PrivateDeletes
	catalog             *catalogChanges
	settings            settingsMark
	isolation           Isolation
	readOnly            bool
	deferrable          bool
	locks               lock.Checkpoint
	cursors             uint64
	exclusion           session.ExclusionMarkSlot // nil when zero; see exclusionMarkSlotOf
	identity            authorization.RoleState
	notificationActions []notificationAction
	notificationEvents  []Notification
}

type settingsMark struct {
	values      map[string]string
	typed       map[string]settings.Value
	actions     map[string]settingAction
	roleAction  roleSettingAction
	currentRole authorization.PrincipalRef
}

func (m settingsMark) clone() settingsMark {
	m.values, m.typed, m.actions = maps.Clone(m.values), maps.Clone(m.typed), maps.Clone(m.actions)
	return m
}

func (s *sessionSettings) savepoint() settingsMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := settingsMark{values: s.values, typed: s.typed, actions: s.actions, roleAction: s.roleAction}
	if s.session != nil {
		s.session.mu.Lock()
		if s.session.authorization != nil {
			m.currentRole = s.session.authorization.state.CurrentUser
		}
		s.session.mu.Unlock()
	}
	return m.clone()
}

// candidate is already independent of the retained mark; no callbacks or
// allocation are allowed between final preflight and effect installation.
func (s *sessionSettings) restoreSavepoint(candidate settingsMark, identity authorization.RoleState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values, s.typed, s.actions = candidate.values, candidate.typed, candidate.actions
	s.roleAction = candidate.roleAction
	if s.session != nil {
		s.session.mu.Lock()
		identity.CurrentUser = candidate.currentRole
		s.restoreIdentityLocked(identity)
		s.session.publishTimeoutSettingsLocked(s, false, time.Now())
		s.session.mu.Unlock()
	}
}

func (t *transactionOwner) truncateSavepoints(n int) {
	clear(t.savepoints[n:])
	t.savepoints = t.savepoints[:n]
}
