package session

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func (s *SettingsStore) pgTrgmAuthority() *SettingsStore {
	if s.pgTrgmOwner != nil {
		return s.pgTrgmOwner
	}
	return s
}

func pgTrgmDefinition(name string) (settings.Definition, bool) {
	for _, d := range settings.PgTrgmDefinitions() {
		if d.Name == name {
			return d, true
		}
	}
	return settings.Definition{}, false
}

// EffectiveDefinitionLocked reads recognized ISN metadata, the immutable
// registry, then the pg_trgm overlay. Placeholders are not real definitions.
func (s *SettingsStore) EffectiveDefinitionLocked(registry settings.Registry, name string) (settings.Definition, bool) {
	name = settings.FoldName(name)
	s.reconcilePgTrgmLocked()
	if name == "isn.weak" && s.IsnLoaded() {
		return IsnDefinition(), true
	}
	if d, ok := registry.Definitions()[name]; ok {
		return d, true
	}
	if s.pgTrgmAuthority().pgTrgmLoaded.Load() {
		return pgTrgmDefinition(name)
	}
	return settings.Definition{}, false
}

// EffectiveSettingNamesLocked borrows registry order until a library loads;
// afterwards it materializes the same ordered union as VisitDefinitionsLocked.
func (s *SettingsStore) EffectiveSettingNamesLocked(registry settings.Registry) []string {
	s.reconcilePgTrgmLocked()
	isnLoaded, pgTrgmLoaded := s.IsnLoaded(), s.pgTrgmAuthority().pgTrgmLoaded.Load()
	if !isnLoaded && !pgTrgmLoaded {
		return registry.Order()
	}
	capacity := len(registry.Order())
	if isnLoaded {
		capacity++
	}
	if pgTrgmLoaded {
		capacity += len(settings.PgTrgmDefinitions())
	}
	names := make([]string, 0, capacity)
	_ = s.VisitDefinitionsLocked(registry, func(d settings.Definition) error {
		names = append(names, d.Name)
		return nil
	})
	return names
}

// ActivatePgTrgmLocked performs the bounded library's first physical load.
// Caller holds Engine.Mu, the physical SettingsStore.Mu, this store's mutex
// (when distinct), and Session.Mu, in that order. Detached ancestors are serial
// invocation-owned. No providers, notices or other callbacks run here; the
// caller emits returned warnings after dropping publication locks.
func (s *SettingsStore) ActivatePgTrgmLocked(registry settings.Registry) ([]*sqlstate.Error, error) {
	if s.Ended {
		return nil, errors.New("stuntgres: settings attachment ended")
	}
	owner := s.pgTrgmAuthority()
	if owner.pgTrgmLoaded.Load() {
		s.reconcilePgTrgmLocked()
		return nil, nil
	}
	for _, d := range settings.PgTrgmDefinitions() {
		if _, exists := registry.Definitions()[d.Name]; exists {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf(`attempt to redefine parameter "%s"`, d.Name)}
		}
	}
	candidate, warnings := preparePgTrgmSettings(s, true)
	// Prepare every active ancestor before publishing any of them. They share
	// historical assignments with the effective image, so only the actual
	// effective replay produces warnings; retained marks remain untouched.
	type promotion struct {
		store, candidate *SettingsStore
	}
	var parents []promotion
	for parent := s.pgTrgmParent; parent != nil && parent != owner; parent = parent.pgTrgmParent {
		if !parent.pgTrgmPromoted {
			image, _ := preparePgTrgmSettings(parent, false)
			parents = append(parents, promotion{parent, image})
		}
	}
	var physical *SettingsStore
	if owner != s && !owner.pgTrgmPromoted {
		physical, _ = preparePgTrgmSettings(owner, false)
	}
	// All preparation is pure; publication below cannot fail.
	s.installPgTrgm(candidate)
	for _, parent := range parents {
		parent.store.installPgTrgm(parent.candidate)
	}
	if physical != nil {
		owner.installPgTrgm(physical)
	}
	owner.pgTrgmLoaded.Store(true)
	return warnings, nil
}

// PgTrgmThresholdLocked reads an active typed value, never a SHOW string and
// never an implicit library load. Caller holds Settings.Mu and Session.Mu.
func (s *SettingsStore) PgTrgmThresholdLocked(registry settings.Registry, name string) (float64, error) {
	name = settings.FoldName(name)
	if !s.pgTrgmAuthority().pgTrgmLoaded.Load() {
		return 0, errors.New("stuntgres: pg_trgm library is not active")
	}
	d, ok := s.EffectiveDefinitionLocked(registry, name)
	if !ok || d.Type != settings.Floating {
		return 0, errors.New("stuntgres: invalid pg_trgm threshold")
	}
	if _, ok := pgTrgmDefinition(name); !ok {
		return 0, errors.New("stuntgres: invalid pg_trgm threshold")
	}
	return s.TypedValue(name).Number, nil
}

func (s *SettingsStore) reconcilePgTrgmLocked() {
	if s.Ended || s.pgTrgmPromoted || !s.pgTrgmAuthority().pgTrgmLoaded.Load() {
		return
	}
	candidate, _ := preparePgTrgmSettings(s, false)
	s.installPgTrgm(candidate)
}

func (s *SettingsStore) installPgTrgm(candidate *SettingsStore) {
	s.Defaults, s.DefaultTyped = candidate.Defaults, candidate.DefaultTyped
	s.DefaultSources, s.ProviderDefaults = candidate.DefaultSources, candidate.ProviderDefaults
	s.Values, s.Typed, s.Actions = candidate.Values, candidate.Typed, candidate.Actions
	s.Placeholders = candidate.Placeholders
	s.defaultsShared, s.pgTrgmPromoted = false, true
}

// preparePgTrgmSettings is a pure image transformer. Every mutable map is
// independent, and replay constructs fresh immutable journal tails. It is also
// used for retained marks/images without emitting a second load warning.
func preparePgTrgmSettings(source *SettingsStore, warn bool) (*SettingsStore, []*sqlstate.Error) {
	out := &SettingsStore{
		Defaults: maps.Clone(source.Defaults), DefaultTyped: maps.Clone(source.DefaultTyped),
		DefaultSources: maps.Clone(source.DefaultSources), ProviderDefaults: maps.Clone(source.ProviderDefaults),
		Values: maps.Clone(source.Values), Typed: maps.Clone(source.Typed), Actions: maps.Clone(source.Actions),
		Placeholders: maps.Clone(source.Placeholders),
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
	var warnings []*sqlstate.Error
	for _, d := range settings.PgTrgmDefinitions() {
		reset := d.BootValue()
		if text, exists := source.Defaults[d.Name]; exists {
			var err error
			reset, err = d.Parse(text)
			if err != nil {
				reset = d.BootValue()
				appendPgTrgmWarning(&warnings, err, warn)
				delete(out.DefaultSources, d.Name)
				delete(out.ProviderDefaults, d.Name)
			}
		}
		out.Defaults[d.Name], out.DefaultTyped[d.Name] = d.Format(reset), reset
		current := settingOverride{Typed: reset}
		if text, present := source.Values[d.Name]; present {
			current = settingOverride{Present: true, Text: text}
		}
		fallback := settingOverride{Typed: reset}
		if a, exists := source.Actions[d.Name]; exists {
			var converted settingAction
			converted, current = replayPgTrgmAction(d, a, current, fallback, &warnings, warn)
			out.Actions[d.Name] = converted
		} else {
			current = promotePgTrgmOverride(d, current, fallback, &warnings, warn)
		}
		if current.Present {
			out.Values[d.Name], out.Typed[d.Name] = current.Text, current.Typed
		} else {
			delete(out.Values, d.Name)
			delete(out.Typed, d.Name)
		}
		delete(out.Placeholders, d.Name)
	}
	for name := range source.Placeholders {
		if !strings.HasPrefix(name, "pg_trgm.") {
			continue
		}
		if _, known := pgTrgmDefinition(name); known {
			continue
		}
		delete(out.Placeholders, name)
		delete(out.Defaults, name)
		delete(out.DefaultTyped, name)
		delete(out.DefaultSources, name)
		delete(out.ProviderDefaults, name)
		delete(out.Values, name)
		delete(out.Typed, name)
		delete(out.Actions, name)
		if warn {
			warnings = append(warnings, &sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s", removing it`, name), Detail: `"pg_trgm" is now a reserved prefix.`})
		}
	}
	return out, warnings
}

func appendPgTrgmWarning(warnings *[]*sqlstate.Error, err error, warn bool) {
	if !warn {
		return
	}
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Severity, copy.SeverityUnlocalized = "WARNING", "WARNING"
		*warnings = append(*warnings, &copy)
	}
}

func promotePgTrgmOverride(d settings.Definition, old, fallback settingOverride, warnings *[]*sqlstate.Error, warn bool) settingOverride {
	if !old.Present {
		return settingOverride{}
	} // Placeholder RESET is internal NULL, not SET ''.
	value, err := d.Parse(old.Text)
	if err != nil {
		appendPgTrgmWarning(warnings, err, warn)
		return fallback
	}
	return settingOverride{Present: true, Text: d.Format(value), Typed: value}
}

// The parent's current value is the child's original. Replay bottom-up before
// installing this level's SET/LOCAL/SAVE; SET_LOCAL's masked SET is a genuine
// assignment and must be checked even when hidden by a valid LOCAL value.
func replayPgTrgmAction(d settings.Definition, old settingAction, current, fallback settingOverride, warnings *[]*sqlstate.Error, warn bool) (settingAction, settingOverride) {
	out := old
	if old.previous != nil {
		previous, original := replayPgTrgmAction(d, *old.previous, old.original, fallback, warnings, warn)
		out.previous, out.original = &previous, original
	} else {
		out.original = promotePgTrgmOverride(d, old.original, fallback, warnings, warn)
	}
	fallback = out.original
	if old.kind == settingSessionLocal {
		out.candidate = promotePgTrgmOverride(d, old.candidate, fallback, warnings, warn)
		fallback = out.candidate
	}
	return out, promotePgTrgmOverride(d, current, fallback, warnings, warn)
}
