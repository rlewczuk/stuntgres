package session

import (
	"maps"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Marks retain immutable logical roots and held lock contributions, not
// generator counters or snapshots. The outer block keeps the same ticket.
type SavepointMark struct {
	Name         string
	Internal     bool   // unnamed procedural boundary, never SQL name lookup
	Serial       uint64 // the owner's savepointSerial at capture; names foreign savepoints
	LargeObjects uint64 // Opening ordinal; cursor motion and explicit closes never roll back.
	Writes       map[ids.ObjectAddress][]storage.RowVersion
	Deletes      map[ids.ObjectAddress]*visibility.PrivateDeletes
	Stats        statistics.Ledger
	StatsLevel   int // actual child nesting, including internal exception marks
	Catalog      *Journal
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
	Values             map[string]string
	Typed              map[string]settings.Value
	Actions            map[string]settingAction
	RoleAction         roleSettingAction
	CurrentRole        roleSetting
	NestLevel          int
	TempTablespaces    *TempTablespaceAssignment
	pgTrgmOwner        *SettingsStore
	pgTrgmPromoted     bool
	pgTrgmDefaults     map[string]string // Immutable bounded pre-load reset capture.
	pgTrgmPlaceholders map[string]bool
	pgcryptoSetter     authorization.PrincipalRef
	pgcryptoAssignment uint64
	pgcryptoOwner      *SettingsStore
	pgcryptoPromoted   bool
}

func (m SettingsMark) Clone() SettingsMark {
	if m.pgTrgmOwner != nil && !m.pgTrgmPromoted && m.pgTrgmOwner.pgTrgmLoaded.Load() {
		candidate, _ := preparePgTrgmSettings(&SettingsStore{
			Defaults: m.pgTrgmDefaults,
			Values:   m.Values, Typed: m.Typed, Actions: m.Actions,
			Placeholders: m.pgTrgmPlaceholders,
		}, false)
		m.Values, m.Typed, m.Actions = candidate.Values, candidate.Typed, candidate.Actions
		m.pgTrgmPromoted = true
	} else {
		m = m.cloneRaw()
	}
	return m.promotePgcrypto(nil, nil)
}

func (m SettingsMark) cloneRaw() SettingsMark {
	m.Values, m.Typed, m.Actions = maps.Clone(m.Values), maps.Clone(m.Typed), maps.Clone(m.Actions)
	return m
}

func (s *SettingsStore) Savepoint() SettingsMark {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	m := SettingsMark{Values: s.Values, Typed: s.Typed, Actions: s.Actions, RoleAction: s.RoleAction, NestLevel: s.NestLevel}
	m.pgcryptoSetter, m.pgcryptoOwner, m.pgcryptoPromoted = s.pgcryptoSetter, s.pgcryptoPhysicalOwner(), s.pgcryptoPromoted
	m.pgcryptoAssignment = s.pgcryptoAssignment
	m.TempTablespaces = s.TempTablespaces
	m.pgTrgmOwner, m.pgTrgmPromoted = s.pgTrgmAuthority(), s.pgTrgmPromoted
	if !s.pgTrgmPromoted {
		for _, d := range settings.PgTrgmDefinitions() {
			if text, exists := s.Defaults[d.Name]; exists {
				if m.pgTrgmDefaults == nil {
					m.pgTrgmDefaults = make(map[string]string, 3)
				}
				m.pgTrgmDefaults[d.Name] = text
			}
		}
		for name := range s.Placeholders {
			if len(name) >= len("pg_trgm.") && name[:len("pg_trgm.")] == "pg_trgm." {
				if m.pgTrgmPlaceholders == nil {
					m.pgTrgmPlaceholders = make(map[string]bool)
				}
				m.pgTrgmPlaceholders[name] = true
			}
		}
	}
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
	beforeStats := s.Value("stats_fetch_consistency")
	s.Values, s.Typed, s.Actions = candidate.Values, candidate.Typed, candidate.Actions
	s.pgcryptoSetter, s.pgcryptoPromoted = candidate.pgcryptoSetter, candidate.pgcryptoPromoted
	s.pgcryptoAssignment = candidate.pgcryptoAssignment
	s.restoreTempTablespaces(candidate.TempTablespaces)
	s.RoleAction = candidate.RoleAction
	s.NestLevel = candidate.NestLevel
	s.RuleRoleChanged(beforeRole)
	s.StatsChanged(beforeStats)
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
	mark.Stats = t.Stats.Clone()
	mark.StatsLevel = len(t.Savepoints) + 1
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
	t.Stats = mark.Stats.Clone()
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
