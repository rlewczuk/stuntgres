package session

import (
	"maps"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Marks retain immutable logical roots and held lock contributions, not
// generator counters or snapshots. The outer block keeps the same ticket.
type SavepointMark struct {
	Name                string
	Internal            bool   // unnamed procedural boundary, never SQL name lookup
	Serial              uint64 // the owner's savepointSerial at capture; names foreign savepoints
	Writes              map[ids.ObjectAddress][]storage.RowVersion
	Deletes             map[ids.ObjectAddress]*visibility.PrivateDeletes
	Catalog             *Journal
	Settings            SettingsMark
	Isolation           Isolation
	ReadOnly            bool
	Deferrable          bool
	Locks               lock.Checkpoint
	Cursors             uint64
	Exclusion           ExclusionMarkSlot // nil when zero; see index.ExclusionMarkSlotOf
	Identity            authorization.RoleState
	NotificationActions []NotificationAction
	NotificationEvents  []Notification
}

type SettingsMark struct {
	Values      map[string]string
	Typed       map[string]settings.Value
	Actions     map[string]settingAction
	RoleAction  roleSettingAction
	CurrentRole authorization.PrincipalRef
}

func (m SettingsMark) Clone() SettingsMark {
	m.Values, m.Typed, m.Actions = maps.Clone(m.Values), maps.Clone(m.Typed), maps.Clone(m.Actions)
	return m
}

func (s *SettingsStore) Savepoint() SettingsMark {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	m := SettingsMark{Values: s.Values, Typed: s.Typed, Actions: s.Actions, RoleAction: s.RoleAction}
	if s.Session != nil {
		s.Session.Mu.Lock()
		if s.Session.Authorization != nil {
			m.CurrentRole = s.Session.Authorization.State.CurrentUser
		}
		s.Session.Mu.Unlock()
	}
	return m.Clone()
}

// candidate is already independent of the retained mark; no callbacks or
// allocation are allowed between final preflight and effect installation.
func (s *SettingsStore) RestoreSavepoint(candidate SettingsMark, identity authorization.RoleState) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Values, s.Typed, s.Actions = candidate.Values, candidate.Typed, candidate.Actions
	s.RoleAction = candidate.RoleAction
	if s.Session != nil {
		s.Session.Mu.Lock()
		identity.CurrentUser = candidate.CurrentRole
		s.RestoreIdentityLocked(identity)
		s.Session.PublishTimeoutSettingsLocked(s, false, time.Now())
		s.Session.Mu.Unlock()
	}
}

func (t *TransactionOwner) TruncateSavepoints(n int) {
	clear(t.Savepoints[n:])
	t.Savepoints = t.Savepoints[:n]
}
