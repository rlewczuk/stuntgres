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
	// Internal exception marks also retain the enclosing statement's COW
	// journal; it is not the transaction journal restored by RollbackToLocked.
	StatementCatalog    *Journal
	Settings            SettingsMark
	Invocation          *InvocationMark // Internal local environment, nil for ordinary savepoints.
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
	CurrentRole roleSetting
	NestLevel   int
}

func (m SettingsMark) Clone() SettingsMark {
	m.Values, m.Typed, m.Actions = maps.Clone(m.Values), maps.Clone(m.Typed), maps.Clone(m.Actions)
	return m
}

func (s *SettingsStore) Savepoint() SettingsMark {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	m := SettingsMark{Values: s.Values, Typed: s.Typed, Actions: s.Actions, RoleAction: s.RoleAction, NestLevel: s.NestLevel}
	if s.Session != nil {
		s.Session.Mu.Lock()
		if s.Session.Authorization != nil {
			m.CurrentRole = captureRole(s.Session.Authorization.State)
		}
		s.Session.Mu.Unlock()
	}
	return m.Clone()
}

// RestoreSavepointLocked installs candidate, which is already independent of
// the retained mark, and identity with candidate's current role. Settings.Mu
// and Session.Mu are held; no callbacks or allocation are allowed between
// final preflight and effect installation.
func (s *SettingsStore) RestoreSavepointLocked(candidate SettingsMark, identity authorization.RoleState) {
	beforeRole := s.TypedValue("session_replication_role").Text
	s.Values, s.Typed, s.Actions = candidate.Values, candidate.Typed, candidate.Actions
	s.RoleAction = candidate.RoleAction
	s.NestLevel = candidate.NestLevel
	s.RuleRoleChanged(beforeRole)
	if s.Session != nil {
		candidate.CurrentRole.restore(&identity)
		s.RestoreIdentityLocked(identity)
		s.Session.PublishTimeoutSettingsLocked(s, false, time.Now())
	}
}

// CaptureSavepoint completes mark, whose name and root values (settings,
// identity, lock checkpoint, cursor ordinal and exclusion slot) the root
// already set, with the owner's private state, and advances the serial.
func (t *TransactionOwner) CaptureSavepoint(mark SavepointMark) SavepointMark {
	writes := maps.Clone(t.Writes)
	for a, rows := range t.Writes {
		// A later append after rollback must not overwrite a discarded suffix
		// still retained by a result opened before rollback.
		writes[a] = rows[:len(rows):len(rows)]
	}
	t.SavepointSerial++
	mark.Serial = t.SavepointSerial
	mark.Writes, mark.Deletes, mark.Catalog = writes, t.Deletes, t.Catalog
	mark.Isolation, mark.ReadOnly, mark.Deferrable = t.Isolation, t.ReadOnly, t.Deferrable
	mark.NotificationActions = t.NotificationActions[:len(t.NotificationActions):len(t.NotificationActions)]
	mark.NotificationEvents = t.NotificationEvents[:len(t.NotificationEvents):len(t.NotificationEvents)]
	return mark
}

// FindSavepoint returns the position of the innermost SQL savepoint named
// name, or -1. Internal procedural marks never match.
func (t *TransactionOwner) FindSavepoint(name string) int {
	for i := len(t.Savepoints) - 1; i >= 0; i-- {
		if !t.Savepoints[i].Internal && t.Savepoints[i].Name == name {
			return i
		}
	}
	return -1
}

// RollbackToLocked restores the owner's private state, settings and
// characteristics from mark, with settings as the independent settings
// candidate. Settings.Mu and Session.Mu are held; the root restores cursors,
// portals, exclusions and SSI around it.
func (t *TransactionOwner) RollbackToLocked(mark SavepointMark, settings SettingsMark) {
	t.Writes, t.Deletes, t.Catalog = mark.Writes, mark.Deletes, mark.Catalog
	t.NotificationActions, t.NotificationEvents = mark.NotificationActions, mark.NotificationEvents
	if mark.Invocation != nil {
		mark.Invocation.Restore()
		return
	}
	store := t.Session.Settings
	store.RestoreSavepointLocked(settings, mark.Identity)
	t.Isolation, t.ReadOnly = mark.Isolation, mark.ReadOnly
	t.Deferrable = mark.Deferrable
	store.CurrentIsolation = IsolationText(t.Isolation)
	store.CurrentReadOnly = t.ReadOnly
	store.CurrentDeferrable = t.Deferrable
}

func (t *TransactionOwner) TruncateSavepoints(n int) {
	clear(t.Savepoints[n:])
	t.Savepoints = t.Savepoints[:n]
}
