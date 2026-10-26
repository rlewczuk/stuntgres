package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

type SettingsStore struct {
	Mu               sync.Mutex
	Session          *Session
	Defaults, Values map[string]string
	Invocation       bool // Detached image: never publish session lifetime timers.
	// typed contains only overrides; captured defaults never require reparsing.
	Typed, DefaultTyped map[string]settings.Value
	Placeholders        map[string]bool
	Ended               bool
	ProviderDefaults    map[string]bool // Captured present responses, even when equal to boot.
	Actions             map[string]settingAction
	RoleAction          roleSettingAction
	// Physical-session sticky simulated TEMP initialization; not journaled.
	// Detached images share the original authority rather than snapshotting it.
	// Atomic access avoids taking the live store mutex under a detached store's
	// publication locks.
	temporaryBuffers     int64
	temporaryBufferOwner *SettingsStore
	// Read-only projections of the transaction owner, published owner-first
	// under this mutex. These are never session overrides or journal actions.
	CurrentIsolation  string
	CurrentReadOnly   bool
	CurrentDeferrable bool
}

// Setting returns the current effective value of a recognized session setting.
// It reads only settings-protected state, including transaction projections.
func (s *Session) Setting(name string) (string, bool) {
	name = settings.FoldName(name)
	store := s.Settings
	store.Mu.Lock()
	defer store.Mu.Unlock()
	if store.Ended {
		return "", false
	}
	_, hasDefault := store.Defaults[name]
	_, hasValue := store.Values[name]
	if !hasDefault && !hasValue && !store.Placeholders[name] {
		return "", false
	}
	return store.Value(name), true
}

type settingOverride struct {
	Present bool
	Text    string
	Typed   settings.Value
}
type settingAction struct {
	original  settingOverride
	candidate settingOverride
	kind      uint8
}

const (
	settingSession uint8 = iota
	settingLocal
	settingSessionLocal
)

// A role has one typed journal entry, not a generic string-setting override.
type roleSettingAction struct {
	Present             bool
	original, candidate authorization.PrincipalRef
	kind                uint8
}

func RoleRevisionHeadroom(a *AuthorizationState, needed uint64) error {
	if ^uint64(0)-a.Revision < needed {
		return fmt.Errorf("stuntgres: session authorization revision exhausted")
	}
	return nil
}

// Called with settings and Session locked. Role refs were validated before
// publication; restoration must not repeat membership eligibility checks.
func (s *SettingsStore) TouchRole(current authorization.PrincipalRef, local bool) {
	a := s.RoleAction
	if !a.Present {
		a.Present = true
		a.original = current
	}
	if !local {
		a.kind = settingSession
		a.candidate = authorization.PrincipalRef{}
	} else if !s.RoleAction.Present {
		a.kind = settingLocal
	} else if a.kind == settingSession {
		a.kind = settingSessionLocal
		a.candidate = current
	}
	s.RoleAction = a
}

// Install a complete boundary state once; admission reserves revision capacity.
func (s *SettingsStore) RestoreIdentityLocked(target authorization.RoleState) {
	a := s.Session.Authorization
	if a != nil && a.State != target {
		a.State = target
		a.Revision++
	}
}

func (s *SettingsStore) override(name string) settingOverride {
	text, present := s.Values[name]
	return settingOverride{Present: present, Text: text, Typed: s.Typed[name]}
}
func (s *SettingsStore) Restore(name string, v settingOverride) {
	if v.Present {
		s.Values[name] = v.Text
		s.Typed[name] = v.Typed
	} else {
		delete(s.Values, name)
		delete(s.Typed, name)
	}
}
func (s *SettingsStore) Touch(name string, local bool) {
	if s.Actions == nil {
		s.Actions = make(map[string]settingAction)
	}
	a, exists := s.Actions[name]
	if !exists {
		a.original = s.override(name)
	}
	if !local {
		a.kind = settingSession
		a.candidate = settingOverride{}
	} else if !exists {
		a.kind = settingLocal
	} else if a.kind == settingSession {
		a.kind = settingSessionLocal
		a.candidate = s.override(name)
		if !a.candidate.Present {
			// PostgreSQL restores a masked SET value with source=session,
			// even when that SET restored a cached default. Preserve the
			// effective typed value, not an absent override or parsed display.
			a.candidate = settingOverride{Present: true, Text: s.Value(name), Typed: s.TypedValue(name)}
		}
	}
	s.Actions[name] = a
}

// finishTransaction runs under transaction admission. Typed timer restoration
// follows settings -> Session ordering, without providers or external callbacks.
func (s *SettingsStore) FinishTransaction(commit bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !s.Ended {
		for name, a := range s.Actions {
			if !commit || a.kind == settingLocal {
				s.Restore(name, a.original)
			} else if a.kind == settingSessionLocal {
				s.Restore(name, a.candidate)
			}
		}
	}
	if s.Session != nil {
		s.Session.Mu.Lock()
		t := s.Session.Transactions
		target := s.Session.Authorization.State
		if !commit && t.IdentityCaptured {
			target.Authenticated = t.IdentityEntry.Authenticated
			target.SessionUser = t.IdentityEntry.SessionUser
			target.ResetRole = t.IdentityEntry.ResetRole
		}
		if a := s.RoleAction; a.Present {
			if !commit || a.kind == settingLocal {
				target.CurrentUser = a.original
			} else if a.kind == settingSessionLocal {
				target.CurrentUser = a.candidate
			}
		}
		s.RestoreIdentityLocked(target)
		t.IdentityEntry = authorization.RoleState{}
		t.IdentityCaptured = false
		s.Session.PublishTimeoutSettingsLocked(s, false, time.Now())
		s.Session.Mu.Unlock()
	}
	s.Actions = nil
	s.RoleAction = roleSettingAction{}
}

func (s *SettingsStore) Initialize(ctx context.Context, definitions map[string]settings.Definition, names []string, provider settings.SessionDefaultProvider) error {
	s.Typed = make(map[string]settings.Value)
	s.DefaultTyped = make(map[string]settings.Value)
	s.Placeholders = make(map[string]bool)
	for _, name := range names {
		d := definitions[name]
		v := d.DefaultValue()
		initial := d.Format(v)
		normalized := initial
		if provider != nil {
			copy := d
			copy.EnumValues = append([]string(nil), d.EnumValues...)
			if d.NativeStartup != nil {
				startup := *d.NativeStartup
				copy.NativeStartup = &startup
			}
			input, present, err := provider.SessionDefault(ctx, copy)
			if err != nil {
				return err
			}
			if present {
				if s.ProviderDefaults == nil {
					s.ProviderDefaults = make(map[string]bool)
				}
				s.ProviderDefaults[name] = true
				v, err = d.Parse(input)
				if err != nil {
					return err
				}
				normalized = d.Format(v)
			}
		}
		if (d.FixedDefault || d.Access == settings.StartupOnly || d.Access == settings.Sighup) && normalized != initial {
			return errors.New("stuntgres: default provider cannot change " + d.Name)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s.Defaults[d.Name] = normalized
		s.DefaultTyped[d.Name] = v
	}
	return nil
}
func (s *SettingsStore) EndSession(_ ids.SessionToken, _ EndReason) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Ended = true
	s.Defaults = nil
	s.Values = nil
	s.Typed = nil
	s.DefaultTyped = nil
	s.Placeholders = nil
	s.ProviderDefaults = nil
	s.Actions = nil
	s.RoleAction = roleSettingAction{}
	atomic.StoreInt64(&s.temporaryBuffers, 0)
	return nil
}
func (s *SettingsStore) Value(name string) string {
	// No startup API can activate archive_mode in this fixed native profile.
	// Keep the stored command empty so provider and reset metadata stay exact.
	if name == "archive_command" {
		return "(disabled)"
	}
	if name == "transaction_isolation" && s.CurrentIsolation != "" {
		return s.CurrentIsolation
	}
	if name == "transaction_read_only" {
		if s.CurrentReadOnly {
			return "on"
		}
		return "off"
	}
	if name == "transaction_deferrable" {
		if s.CurrentDeferrable {
			return "on"
		}
		return "off"
	}
	if v, ok := s.Values[name]; ok {
		return v
	}
	return s.Defaults[name]
}
func (s *SettingsStore) TypedValue(name string) settings.Value {
	if v, ok := s.Typed[name]; ok {
		return v
	}
	return s.DefaultTyped[name]
}

func (s *SettingsStore) temporaryBufferAuthority() *SettingsStore {
	if s.temporaryBufferOwner != nil {
		return s.temporaryBufferOwner
	}
	return s
}

// InitializeTemporaryBuffers captures the effective configured count at the first
// owned logical TEMP table data access, even from a detached invocation image.
// Transaction, invocation and exception restoration never clear this authority.
func (s *SettingsStore) InitializeTemporaryBuffers() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !s.Ended {
		atomic.CompareAndSwapInt64(&s.temporaryBufferAuthority().temporaryBuffers, 0, int64(s.TypedValue("temp_buffers").Number))
	}
}
