package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

type sessionSettings struct {
	mu               sync.Mutex
	session          *Session
	defaults, values map[string]string
	// typed contains only overrides; captured defaults never require reparsing.
	typed, defaultTyped        map[string]settings.Value
	placeholders               map[string]bool
	ended                      bool
	rowSecurityProviderDefault bool // Immutable native startup-default provenance.
	actions                    map[string]settingAction
	roleAction                 roleSettingAction
	// Read-only projections of the transaction owner, published owner-first
	// under this mutex. These are never session overrides or journal actions.
	currentIsolation  string
	currentReadOnly   bool
	currentDeferrable bool
}

// Setting returns the current effective value of a recognized session setting.
// It reads only settings-protected state, including transaction projections.
func (s *Session) Setting(name string) (string, bool) {
	name = settings.FoldName(name)
	store := s.settings
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.ended {
		return "", false
	}
	_, hasDefault := store.defaults[name]
	_, hasValue := store.values[name]
	if !hasDefault && !hasValue && !store.placeholders[name] {
		return "", false
	}
	return store.value(name), true
}

type settingOverride struct {
	present bool
	text    string
	typed   settings.Value
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
	present             bool
	original, candidate authorization.PrincipalRef
	kind                uint8
}

func roleRevisionHeadroom(a *sessionAuthorization, needed uint64) error {
	if ^uint64(0)-a.revision < needed {
		return fmt.Errorf("stuntgres: session authorization revision exhausted")
	}
	return nil
}

// Called with settings and Session locked. Role refs were validated before
// publication; restoration must not repeat membership eligibility checks.
func (s *sessionSettings) touchRole(current authorization.PrincipalRef, local bool) {
	a := s.roleAction
	if !a.present {
		a.present = true
		a.original = current
	}
	if !local {
		a.kind = settingSession
		a.candidate = authorization.PrincipalRef{}
	} else if !s.roleAction.present {
		a.kind = settingLocal
	} else if a.kind == settingSession {
		a.kind = settingSessionLocal
		a.candidate = current
	}
	s.roleAction = a
}

// Install a complete boundary state once; admission reserves revision capacity.
func (s *sessionSettings) restoreIdentityLocked(target authorization.RoleState) {
	a := s.session.authorization
	if a != nil && a.state != target {
		a.state = target
		a.revision++
	}
}

func (s *sessionSettings) override(name string) settingOverride {
	text, present := s.values[name]
	return settingOverride{present: present, text: text, typed: s.typed[name]}
}
func (s *sessionSettings) restore(name string, v settingOverride) {
	if v.present {
		s.values[name] = v.text
		s.typed[name] = v.typed
	} else {
		delete(s.values, name)
		delete(s.typed, name)
	}
}
func (s *sessionSettings) touch(name string, local bool) {
	if s.actions == nil {
		s.actions = make(map[string]settingAction)
	}
	a, exists := s.actions[name]
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
	}
	s.actions[name] = a
}

// finishTransaction runs under transaction admission. Typed timer restoration
// follows settings -> Session ordering, without providers or external callbacks.
func (s *sessionSettings) finishTransaction(commit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		for name, a := range s.actions {
			if !commit || a.kind == settingLocal {
				s.restore(name, a.original)
			} else if a.kind == settingSessionLocal {
				s.restore(name, a.candidate)
			}
		}
	}
	if s.session != nil {
		s.session.mu.Lock()
		t := s.session.transactions
		target := s.session.authorization.state
		if !commit && t.identityCaptured {
			target.Authenticated = t.identityEntry.Authenticated
			target.SessionUser = t.identityEntry.SessionUser
			target.ResetRole = t.identityEntry.ResetRole
		}
		if a := s.roleAction; a.present {
			if !commit || a.kind == settingLocal {
				target.CurrentUser = a.original
			} else if a.kind == settingSessionLocal {
				target.CurrentUser = a.candidate
			}
		}
		s.restoreIdentityLocked(target)
		t.identityEntry = authorization.RoleState{}
		t.identityCaptured = false
		s.session.publishTimeoutSettingsLocked(s, false, time.Now())
		s.session.mu.Unlock()
	}
	s.actions = nil
	s.roleAction = roleSettingAction{}
}

func (s *sessionSettings) initialize(ctx context.Context, definitions map[string]settings.Definition, names []string, provider settings.SessionDefaultProvider) error {
	s.typed = make(map[string]settings.Value)
	s.defaultTyped = make(map[string]settings.Value)
	s.placeholders = make(map[string]bool)
	for _, name := range names {
		d := definitions[name]
		v := d.BootValue()
		normalized := d.BootDefault
		if provider != nil {
			copy := d
			copy.EnumValues = append([]string(nil), d.EnumValues...)
			input, present, err := provider.SessionDefault(ctx, copy)
			if err != nil {
				return err
			}
			if present {
				if name == "row_security" {
					s.rowSecurityProviderDefault = true
				}
				v, err = d.Parse(input)
				if err != nil {
					return err
				}
				normalized = d.Format(v)
			}
		}
		if (d.Access == settings.StartupOnly || d.Name == "server_encoding" || d.Name == "log_statement_stats") && normalized != d.BootDefault {
			return errors.New("stuntgres: default provider cannot change " + d.Name)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s.defaults[d.Name] = normalized
		s.defaultTyped[d.Name] = v
	}
	return nil
}
func (s *sessionSettings) EndSession(_ ids.SessionToken, _ EndReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	s.defaults = nil
	s.values = nil
	s.typed = nil
	s.defaultTyped = nil
	s.placeholders = nil
	s.actions = nil
	s.roleAction = roleSettingAction{}
	return nil
}
func (s *sessionSettings) value(name string) string {
	if name == "transaction_isolation" && s.currentIsolation != "" {
		return s.currentIsolation
	}
	if name == "transaction_read_only" {
		if s.currentReadOnly {
			return "on"
		}
		return "off"
	}
	if name == "transaction_deferrable" {
		if s.currentDeferrable {
			return "on"
		}
		return "off"
	}
	if v, ok := s.values[name]; ok {
		return v
	}
	return s.defaults[name]
}
func (s *sessionSettings) typedValue(name string) settings.Value {
	if v, ok := s.typed[name]; ok {
		return v
	}
	return s.defaultTyped[name]
}
