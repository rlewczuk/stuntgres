package session

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

type autoExplainSetter struct {
	principal  authorization.PrincipalRef
	assignment uint64
	superuser  bool
}
type autoExplainSetterKey struct {
	name      string
	principal authorization.PrincipalRef
}
type autoExplainReplay struct {
	mu          sync.Mutex // Collection only; permissions are immutable after loaded publication.
	assignment  uint64
	setters     map[autoExplainSetterKey]struct{}
	permissions map[autoExplainSetterKey]bool
}

func (s *SettingsStore) AutoExplainAssignmentHeadroomLocked(name string, present bool) error {
	if !present || s.AutoExplainLoaded() {
		return nil
	}
	if _, selected := autoExplainDefinition(name); !selected {
		return nil
	}
	m := &s.autoExplainAuthority().autoExplainReplay
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.assignment == ^uint64(0) {
		return errors.New("stuntgres: auto_explain setting assignment identity exhausted")
	}
	return nil
}
func (s *SettingsStore) CaptureAutoExplainSetterLocked(name string, principal authorization.PrincipalRef, superuser, present bool) {
	if _, selected := autoExplainDefinition(name); !selected {
		return
	}
	if !present || s.AutoExplainLoaded() {
		delete(s.autoExplainSetters, name)
		return
	}
	m := &s.autoExplainAuthority().autoExplainReplay
	m.mu.Lock()
	m.assignment++
	assignment := m.assignment
	if !superuser {
		if m.setters == nil {
			m.setters = make(map[autoExplainSetterKey]struct{})
		}
		m.setters[autoExplainSetterKey{name, principal}] = struct{}{}
	}
	m.mu.Unlock()
	if s.autoExplainSetters == nil {
		s.autoExplainSetters = make(map[string]autoExplainSetter)
	}
	s.autoExplainSetters[name] = autoExplainSetter{principal, assignment, superuser}
}

type autoExplainWarning func(sqlstate.Error, pgcryptoReplayKey)

// prepareAutoExplain is a pure image transformer. Replay deliberately uses the
// same successful-action reconstruction as pgcrypto, not parse-only conversion:
// a denied/invalid assignment creates no journal node and retains its predecessor.
func prepareAutoExplain(source *SettingsStore, emit autoExplainWarning) *SettingsStore {
	out := &SettingsStore{
		Defaults: maps.Clone(source.Defaults), DefaultTyped: maps.Clone(source.DefaultTyped),
		DefaultSources: maps.Clone(source.DefaultSources), ProviderDefaults: maps.Clone(source.ProviderDefaults),
		Values: maps.Clone(source.Values), Typed: maps.Clone(source.Typed), Actions: maps.Clone(source.Actions),
		Placeholders: maps.Clone(source.Placeholders), autoExplainSetters: maps.Clone(source.autoExplainSetters),
		NestLevel: source.NestLevel, autoExplainPromoted: true,
	}
	if out.Defaults == nil {
		out.Defaults = make(map[string]string)
	}
	if out.DefaultTyped == nil {
		out.DefaultTyped = make(map[string]settings.Value)
	}
	if out.Values == nil {
		out.Values = make(map[string]string)
	}
	if out.Typed == nil {
		out.Typed = make(map[string]settings.Value)
	}
	permissions := source.autoExplainAuthority().autoExplainReplay.permissions
	for _, d := range settings.AutoExplainDefinitions() {
		convert := func(v settingOverride, level int, action SettingAction) (settingOverride, bool) {
			if !v.Present {
				return v, true
			}
			var err error
			if !v.SuperuserContext && v.Setter != (authorization.PrincipalRef{}) && !permissions[autoExplainSetterKey{d.Name, v.Setter}] {
				err = sqlError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, d.Name))
			} else {
				v.Typed, err = d.Parse(v.Text)
			}
			if err != nil {
				var diagnostic *sqlstate.Error
				if emit != nil && errors.As(err, &diagnostic) {
					emit(*diagnostic, pgcryptoReplayKey{name: d.Name, value: v.Text, setter: v.Setter, assignment: v.Assignment, level: level, action: action})
				}
				return settingOverride{}, false
			}
			v.Text, v.Setter, v.Assignment, v.SuperuserContext = d.Format(v.Typed), authorization.PrincipalRef{}, 0, false
			return v, true
		}
		reset := d.BootValue()
		if text, present := source.Defaults[d.Name]; present {
			if v, valid := convert(settingOverride{Present: true, Text: text, SuperuserContext: true}, -1, SettingSet); valid {
				reset = v.Typed
			} else {
				delete(out.DefaultSources, d.Name)
				delete(out.ProviderDefaults, d.Name)
			}
		}
		out.Defaults[d.Name], out.DefaultTyped[d.Name] = d.Format(reset), reset
		current, action := source.override(d.Name), source.Actions[d.Name]
		_, stacked := source.Actions[d.Name]
		delete(out.Values, d.Name)
		delete(out.Typed, d.Name)
		delete(out.Actions, d.Name)
		delete(out.autoExplainSetters, d.Name)
		apply := func(raw settingOverride, level int, action SettingAction, journal bool) {
			v, valid := convert(raw, level, action)
			if !valid {
				return
			}
			if journal {
				out.NestLevel = level
				out.Touch(d.Name, action)
			}
			if v.Present {
				out.Values[d.Name], out.Typed[d.Name] = v.Text, v.Typed
			} else {
				delete(out.Values, d.Name)
				delete(out.Typed, d.Name)
			}
		}
		var replay func(*settingAction, settingOverride)
		replay = func(a *settingAction, v settingOverride) {
			if a == nil {
				if v.Present {
					apply(v, 0, SettingSet, false)
				}
				return
			}
			replay(a.previous, a.original)
			switch a.kind {
			case settingSave:
				apply(v, a.level, SettingSave, true)
			case settingSession:
				apply(v, a.level, SettingSet, true)
			case settingLocal:
				apply(v, a.level, SettingLocal, true)
			case settingSessionLocal:
				apply(a.candidate, a.level, SettingSet, true)
				apply(v, a.level, SettingLocal, true)
			}
		}
		if stacked {
			replay(&action, current)
		} else {
			replay(nil, current)
		}
		out.NestLevel = source.NestLevel
		delete(out.Placeholders, d.Name)
	}
	var removed []string
	for name := range out.Placeholders {
		if strings.HasPrefix(name, "auto_explain.") {
			if _, known := autoExplainDefinition(name); !known {
				removed = append(removed, name)
			}
		}
	}
	sort.Strings(removed)
	for _, name := range removed {
		delete(out.Placeholders, name)
		delete(out.Defaults, name)
		delete(out.DefaultTyped, name)
		delete(out.DefaultSources, name)
		delete(out.ProviderDefaults, name)
		delete(out.Values, name)
		delete(out.Typed, name)
		delete(out.Actions, name)
		if emit != nil {
			emit(sqlstate.Error{Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s", removing it`, name), Detail: `"auto_explain" is now a reserved prefix.`}, pgcryptoReplayKey{name: name})
		}
	}
	return out
}
