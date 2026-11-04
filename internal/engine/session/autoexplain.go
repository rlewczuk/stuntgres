package session

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func (s *SettingsStore) autoExplainAuthority() *SettingsStore {
	if s.autoExplainOwner != nil {
		return s.autoExplainOwner
	}
	return s
}
func (s *SettingsStore) AutoExplainLoaded() bool {
	return !s.Ended && s.autoExplainAuthority().autoExplainLoaded.Load()
}
func autoExplainDefinition(name string) (settings.Definition, bool) {
	if !strings.HasPrefix(name, "auto_explain.") {
		return settings.Definition{}, false
	}
	for _, d := range settings.AutoExplainDefinitions() {
		if d.Name == name {
			return d, true
		}
	}
	return settings.Definition{}, false
}
func (s *SettingsStore) installAutoExplain(candidate *SettingsStore) {
	s.Defaults, s.DefaultTyped = candidate.Defaults, candidate.DefaultTyped
	s.DefaultSources, s.ProviderDefaults = candidate.DefaultSources, candidate.ProviderDefaults
	s.Values, s.Typed, s.Actions = candidate.Values, candidate.Typed, candidate.Actions
	s.Placeholders, s.autoExplainSetters = candidate.Placeholders, candidate.autoExplainSetters
	s.defaultsShared, s.autoExplainPromoted = false, true
}
func (s *SettingsStore) reconcileAutoExplainLocked() {
	if s.autoExplainPromoted || !s.AutoExplainLoaded() {
		return
	}
	s.installAutoExplain(prepareAutoExplain(s, nil))
}

// ActivateAutoExplainLocked requires Transaction -> Engine -> physical settings
// -> effective settings -> Session locks. allow is an internal pure query over
// one captured role/catalog root, never an application callback. Old marks are
// replaced by owned promoted images; their retained originals stay untouched.
func (s *SettingsStore) ActivateAutoExplainLocked(op *Operation, registry settings.Registry, allow func(string, authorization.PrincipalRef) (bool, error)) ([]*sqlstate.Error, error) {
	if s.Ended {
		return nil, errors.New("stuntgres: settings attachment ended")
	}
	owner := s.autoExplainAuthority()
	if owner.autoExplainLoaded.Load() {
		s.reconcileAutoExplainLocked()
		return nil, nil
	}
	for _, d := range settings.AutoExplainDefinitions() {
		if _, exists := registry.Definitions()[d.Name]; exists {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf(`attempt to redefine parameter "%s"`, d.Name)}
		}
	}
	m := &owner.autoExplainReplay
	m.mu.Lock()
	defer m.mu.Unlock()
	permissions := make(map[autoExplainSetterKey]bool, len(m.setters))
	for key := range m.setters {
		allowed, err := allow(key.name, key.principal)
		if err != nil {
			return nil, err
		}
		permissions[key] = allowed
	}
	if op != nil {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
	}
	// All remaining transformations are pure/infallible. Publishing immutable
	// replay authority first permits ordinary lazy reconciliation of old marks.
	m.permissions, m.setters = permissions, nil
	owner.autoExplainLoaded.Store(true)
	type loadWarning struct {
		name       string
		diagnostic sqlstate.Error
	}
	var pending []loadWarning
	seen := make(map[pgcryptoReplayKey]bool)
	emit := func(d sqlstate.Error, key pgcryptoReplayKey) {
		if seen[key] {
			return
		}
		seen[key] = true
		d.Severity, d.SeverityUnlocalized = "WARNING", "WARNING"
		pending = append(pending, loadWarning{key.name, d})
	}
	if op != nil {
		marks := slices.Clone(op.Session.Transactions.Savepoints)
		for i := range marks {
			marks[i].Settings = marks[i].Settings.promoteAutoExplain(emit)
			if original := marks[i].Invocation; original != nil {
				next := *original
				next.image = cloneInvocationSettingsLocked(original.image)
				// Clone's lazy promotion is silent; replay original once with
				// shared assignment identities to preserve warning multiplicity.
				if !original.image.autoExplainPromoted {
					next.image.installAutoExplain(prepareAutoExplain(original.image, emit))
				}
				marks[i].Invocation = &next
			}
		}
		op.Session.Transactions.Savepoints = marks
	}
	for parent := s.autoExplainParent; parent != nil && parent != owner; parent = parent.autoExplainParent {
		if !parent.autoExplainPromoted {
			parent.installAutoExplain(prepareAutoExplain(parent, emit))
		}
	}
	if owner != s && !owner.autoExplainPromoted {
		owner.installAutoExplain(prepareAutoExplain(owner, emit))
	}
	s.installAutoExplain(prepareAutoExplain(s, emit))
	// Definitions are installed in release order, before prefix reservation.
	// Active image traversal must not reorder their original-assignment warnings.
	slices.SortStableFunc(pending, func(a, b loadWarning) int {
		difference := autoExplainLoadRank(a.name) - autoExplainLoadRank(b.name)
		if difference != 0 {
			return difference
		}
		if autoExplainLoadRank(a.name) == 12 {
			return strings.Compare(a.name, b.name)
		}
		return 0
	})
	var warnings []*sqlstate.Error
	if len(pending) != 0 {
		warnings = make([]*sqlstate.Error, len(pending))
		for i := range pending {
			warnings[i] = &pending[i].diagnostic
		}
	}
	return warnings, nil
}

func (m SettingsMark) promoteAutoExplain(emit autoExplainWarning) SettingsMark {
	if m.autoExplainOwner == nil || m.autoExplainPromoted || !m.autoExplainOwner.autoExplainLoaded.Load() {
		return m
	}
	candidate := prepareAutoExplain(&SettingsStore{Defaults: m.autoExplainDefaults, Values: m.Values, Typed: m.Typed, Actions: m.Actions, Placeholders: m.autoExplainPlaceholders, autoExplainSetters: m.autoExplainSetters, autoExplainOwner: m.autoExplainOwner, NestLevel: m.NestLevel}, emit)
	m.Values, m.Typed, m.Actions = candidate.Values, candidate.Typed, candidate.Actions
	m.autoExplainSetters = nil
	m.autoExplainPromoted = true
	return m
}

func autoExplainLoadRank(name string) int {
	switch name {
	case "auto_explain.log_min_duration":
		return 0
	case "auto_explain.log_parameter_max_length":
		return 1
	case "auto_explain.log_analyze":
		return 2
	case "auto_explain.log_verbose":
		return 3
	case "auto_explain.log_buffers":
		return 4
	case "auto_explain.log_wal":
		return 5
	case "auto_explain.log_triggers":
		return 6
	case "auto_explain.log_format":
		return 7
	case "auto_explain.log_level":
		return 8
	case "auto_explain.log_nested_statements":
		return 9
	case "auto_explain.log_timing":
		return 10
	case "auto_explain.sample_rate":
		return 11
	}
	return 12
}
