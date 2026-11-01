package session

import (
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// IsnDefinition is module metadata, not a global registry entry. Recognition is
// owned by the physical SettingsStore and survives transaction/catalog changes.
func IsnDefinition() settings.Definition {
	return settings.Definition{Name: "isn.weak", Type: settings.Boolean, Access: settings.Session, BootDefault: "off", Category: "Customized Options", Description: "Accept input with invalid ISN check digits."}
}

func (s *SettingsStore) isnAuthority() *SettingsStore {
	if s.isnOwner != nil {
		return s.isnOwner
	}
	return s
}

func (s *SettingsStore) IsnLoaded() bool { return s.isnAuthority().isnLoaded.Load() }

// LoadIsnLocked promotes this effective image using the ordinary setting journal;
// it neither pushes an assignment nor changes the session reset default. Caller
// holds the effective store mutex. The returned warning is delivered unlocked.
func (s *SettingsStore) LoadIsnLocked() *sqlstate.Error {
	first := !s.isnAuthority().isnLoaded.Swap(true)
	if s.isnPromoted {
		return nil
	}
	s.isnPromoted = true
	warning := promoteIsnImage(s.Values, s.Typed, s.Actions)
	delete(s.Placeholders, "isn.weak")
	if !first {
		return nil
	}
	return warning
}

// PrepareSavepointRestore owns all copying and promotion before rollback's final
// callback-free/allocation-free publication window. The retained mark is intact.
func (s *SettingsStore) PrepareSavepointRestore(mark SettingsMark) SettingsMark {
	candidate := mark.Clone()
	if s.IsnLoaded() {
		promoteIsnImage(candidate.Values, candidate.Typed, candidate.Actions)
	}
	return candidate
}

func promoteIsnImage(values map[string]string, typed map[string]settings.Value, actions map[string]settingAction) *sqlstate.Error {
	var warning *sqlstate.Error
	promote := func(v settingOverride) settingOverride {
		if !v.Present {
			return v
		}
		parsed, err := IsnDefinition().Parse(v.Text)
		if err != nil {
			warning = &sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `parameter "isn.weak" requires a Boolean value`}
			return settingOverride{}
		}
		v.Typed, v.Text = parsed, IsnDefinition().Format(parsed)
		return v
	}
	text, present := values["isn.weak"]
	current := promote(settingOverride{Present: present, Text: text, Typed: typed["isn.weak"]})
	if current.Present {
		values["isn.weak"], typed["isn.weak"] = current.Text, current.Typed
	} else {
		delete(values, "isn.weak")
		delete(typed, "isn.weak")
	}
	if action, ok := actions["isn.weak"]; ok {
		var copyAction func(settingAction) settingAction
		copyAction = func(a settingAction) settingAction {
			a.original, a.candidate = promote(a.original), promote(a.candidate)
			if a.previous != nil {
				previous := copyAction(*a.previous)
				a.previous = &previous
			}
			return a
		}
		actions["isn.weak"] = copyAction(action)
	}
	return warning
}

// VisitDefinitionsLocked exposes the immutable registry plus recognized module
// metadata to SHOW ALL and pg_settings without copying or mutating the registry.
func (s *SettingsStore) VisitDefinitionsLocked(registry settings.Registry, visit func(settings.Definition) error) error {
	loaded, inserted := s.IsnLoaded(), false
	for _, name := range registry.Order() {
		if loaded && !inserted && name >= "isn.weak" {
			if err := visit(IsnDefinition()); err != nil { return err }
			inserted = true
		}
		if loaded && name == "isn.weak" {
			continue
		}
		if err := visit(registry.Definitions()[name]); err != nil { return err }
	}
	if loaded && !inserted {
		return visit(IsnDefinition())
	}
	return nil
}
