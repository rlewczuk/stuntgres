package session

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// The physical owner publishes this attachment once. Detached invocation and
// savepoint images share the attachment, never its transaction-local values.
// mu protects pre-load setter collection; loaded publishes immutable recipe and
// replay authority. No operation, catalog view, callback or provider is retained.
type pgcryptoModule struct {
	mu sync.Mutex
	loaded atomic.Bool
	definition settings.Definition
	setters map[authorization.PrincipalRef]struct{}
	permissions map[authorization.PrincipalRef]bool
	registeredPrefix map[string]bool // Immutable genuine definitions, never placeholders.
	assignment uint64
}

func (s *SettingsStore) pgcryptoAuthority() *pgcryptoModule {
	if s.pgcryptoOwner != nil { return &s.pgcryptoOwner.pgcrypto }
	return &s.pgcrypto
}

func (s *SettingsStore) pgcryptoPhysicalOwner() *SettingsStore {
	if s.pgcryptoOwner != nil { return s.pgcryptoOwner }
	return s
}

// PgcryptoInitialized is the runtime's cheap idempotence check. It exposes no
// descriptor, values, replay principals or live authority.
func (s *SettingsStore) PgcryptoInitialized() bool { return s.pgcryptoAuthority().loaded.Load() }

// PgcryptoAssignmentHeadroomLocked precedes successful setting publication.
// The physical Session lock serializes every effective invocation image.
func (s *SettingsStore) PgcryptoAssignmentHeadroomLocked(name string, present bool) error {
	if name != settings.PgcryptoBuiltinCrypto || !present { return nil }
	m := s.pgcryptoAuthority()
	if m.loaded.Load() { return nil }
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.assignment == ^uint64(0) { return fmt.Errorf("stuntgres: pgcrypto setting assignment identity exhausted") }
	return nil
}

// CapturePgcryptoSetterLocked captures the actual successful original setter,
// not CREATE EXTENSION's later bootstrap member owner. Caller holds the setting
// and Session publication locks. This is inert for ordinary registry settings.
func (s *SettingsStore) CapturePgcryptoSetterLocked(name string, setter authorization.PrincipalRef, present bool) {
	if name != settings.PgcryptoBuiltinCrypto { return }
	if !present { s.pgcryptoSetter, s.pgcryptoAssignment = authorization.PrincipalRef{}, 0; return }
	m := s.pgcryptoAuthority()
	if m.loaded.Load() { s.pgcryptoSetter, s.pgcryptoAssignment = authorization.PrincipalRef{}, 0; return }
	m.mu.Lock()
	m.assignment++
	s.pgcryptoAssignment = m.assignment
	if m.setters == nil { m.setters = make(map[authorization.PrincipalRef]struct{}) }
	m.setters[setter] = struct{}{}
	m.mu.Unlock()
	s.pgcryptoSetter = setter
}


// InitializePgcrypto owns the one physical transition. The caller holds the
// transaction owner's Mu, then Engine.Mu; allow is an internal pure query over
// that captured catalog/role root. No user callback or notice handler is called
// here. Owned diagnostics return through the ordinary operation filter.
func (s *SettingsStore) InitializePgcrypto(op *Operation, domain *ids.Domain, registry settings.Registry, allow func(authorization.PrincipalRef) (bool, error), scriptContext string) ([]sqlstate.Error, error) {
	effective := op.EffectiveSettings()
	m := s.pgcryptoAuthority()
	var marks []SavepointMark
	if !m.loaded.Load() && len(op.Session.Transactions.Savepoints) != 0 {
		// Actual active savepoints retain distinct assignments even where the
		// ordinary flat journal coalesces LOCAL actions at the same level.
		marks = slices.Clone(op.Session.Transactions.Savepoints)
		for i := range marks {
			marks[i].Settings = marks[i].Settings.cloneRaw()
			if original := marks[i].Invocation; original != nil {
				next := *original
				original.image.Mu.Lock()
				next.image = cloneInvocationSettingsLocked(original.image)
				original.image.Mu.Unlock()
				marks[i].Invocation = &next
			}
		}
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if effective != s { effective.Mu.Lock(); defer effective.Mu.Unlock() }
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := s.AdmitLocked(domain, op); err != nil { return nil, err }
	if m.loaded.Load() {
		s.syncPgcryptoLocked(nil)
		if effective != s { effective.syncPgcryptoLocked(nil) }
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var permissions map[authorization.PrincipalRef]bool
	if len(m.setters) != 0 { permissions = make(map[authorization.PrincipalRef]bool, len(m.setters)) }
	for setter := range m.setters {
		allowed, err := allow(setter)
		if err != nil { return nil, err }
		permissions[setter] = allowed
	}
	var registeredPrefix map[string]bool
	for _, name := range registry.Order() {
		if strings.HasPrefix(name, "pgcrypto.") {
			if registeredPrefix == nil { registeredPrefix = make(map[string]bool) }
			registeredPrefix[name] = true
		}
	}
	if err := op.Context().Err(); err != nil { return nil, err }
	m.definition, m.permissions, m.registeredPrefix = settings.PgcryptoDefinition(), permissions, registeredPrefix
	m.setters = nil
	m.loaded.Store(true)
	var notices, removals []sqlstate.Error
	seen := make(map[pgcryptoReplayKey]bool)
	emit := func(n sqlstate.Error, key pgcryptoReplayKey) {
		if seen[key] { return }
		seen[key] = true
		n.Severity, n.SeverityUnlocalized, n.Context = "WARNING", "WARNING", scriptContext
		if key.name == settings.PgcryptoBuiltinCrypto { notices = append(notices, n) } else { removals = append(removals, n) }
	}
	for i := range marks {
		marks[i].Settings = marks[i].Settings.promotePgcrypto(emit, s)
		if mark := marks[i].Invocation; mark != nil { mark.image.syncPgcryptoLocked(emit) }
	}
	s.syncPgcryptoLocked(emit)
	if effective != s { effective.syncPgcryptoLocked(emit) }
	if marks != nil { op.Session.Transactions.Savepoints = marks }
	return append(notices, removals...), nil
}

type pgcryptoReplayKey struct {
	name, value string
	setter authorization.PrincipalRef
	assignment uint64
	level int
	action SettingAction
}

// promotePgcrypto operates on an already owned mark, never its retained backing.
// Initialization supplies the raw reset entry so its diagnostic precedes every
// historical/current replay; ordinary later Clone needs only overrides/actions.
func (m SettingsMark) promotePgcrypto(emit func(sqlstate.Error, pgcryptoReplayKey), reset *SettingsStore) SettingsMark {
	if m.pgcryptoOwner == nil || m.pgcryptoPromoted || !m.pgcryptoOwner.pgcryptoAuthority().loaded.Load() { return m }
	image := &SettingsStore{Values: m.Values, Typed: m.Typed, Actions: m.Actions, pgcryptoSetter: m.pgcryptoSetter, pgcryptoAssignment: m.pgcryptoAssignment, pgcryptoOwner: m.pgcryptoOwner}
	if reset != nil {
		name := settings.PgcryptoBuiltinCrypto
		if text, present := reset.Defaults[name]; present {
			image.Defaults = map[string]string{name: text}
			image.DefaultTyped = map[string]settings.Value{name: reset.DefaultTyped[name]}
		}
	}
	image.syncPgcryptoLocked(emit)
	m.Values, m.Typed, m.Actions = image.Values, image.Typed, image.Actions
	m.pgcryptoSetter, m.pgcryptoAssignment = image.pgcryptoSetter, image.pgcryptoAssignment
	m.pgcryptoPromoted = true
	return m
}

// Old savepoint/exception images may first become current after initialization.
// Conversion uses the original assignment and authority frozen at that physical
// transition, not today's caller/grants. It never unloads or emits notices twice.
func (s *SettingsStore) syncPgcryptoLocked(emit func(sqlstate.Error, pgcryptoReplayKey)) {
	m := s.pgcryptoAuthority()
	if s.pgcryptoPromoted || !m.loaded.Load() || s.Ended { return }
	d := m.definition
	if s.defaultsShared {
		s.Defaults, s.DefaultTyped = maps.Clone(s.Defaults), maps.Clone(s.DefaultTyped)
		s.defaultsShared = false
	}
	if s.Defaults == nil { s.Defaults = make(map[string]string) }
	if s.DefaultTyped == nil { s.DefaultTyped = make(map[string]settings.Value) }
	if s.Values == nil { s.Values = make(map[string]string) }
	if s.Typed == nil { s.Typed = make(map[string]settings.Value) }
	convert := func(v settingOverride, fallback settingOverride, level int, action SettingAction) (settingOverride, bool) {
		if !v.Present { return v, true }
		var failure error
		if v.Setter != (authorization.PrincipalRef{}) && !m.permissions[v.Setter] {
			failure = sqlError("42501", `permission denied to set parameter "pgcrypto.builtin_crypto_enabled"`)
		} else {
			v.Typed, failure = d.Parse(v.Text)
		}
		if failure != nil {
			if emit != nil {
				var n *sqlstate.Error
				if errors.As(failure, &n) { emit(*n, pgcryptoReplayKey{name: d.Name, value: v.Text, setter: v.Setter, assignment: v.Assignment, level: level, action: action}) }
			}
			return fallback, false
		}
		v.Text = d.Format(v.Typed)
		v.Setter = authorization.PrincipalRef{}
		v.Assignment = 0
		return v, true
	}
	reset := settingOverride{Present: true, Text: d.BootDefault, Typed: d.DefaultValue()}
	if text, ok := s.Defaults[d.Name]; ok {
		var valid bool
		reset, valid = convert(settingOverride{Present: true, Text: text, Typed: s.DefaultTyped[d.Name]}, reset, -1, SettingSet)
		if !valid { delete(s.DefaultSources, d.Name); delete(s.ProviderDefaults, d.Name) }
	}
	s.Defaults[d.Name], s.DefaultTyped[d.Name] = reset.Text, reset.Typed
	// Reapply logical actions bottom-to-top, just like define_custom_variable:
	// failed replay does not create a new stack node or overwrite prior state.
	// In particular SET_LOCAL replays its masked SET before its current LOCAL.
	rawCurrent := s.override(d.Name)
	rawAction, stacked := s.Actions[d.Name]
	delete(s.Actions, d.Name)
	delete(s.Values, d.Name)
	delete(s.Typed, d.Name)
	s.pgcryptoSetter, s.pgcryptoAssignment = authorization.PrincipalRef{}, 0
	s.pgcryptoPromoted = true // Touch may read the already initialized default.
	nestLevel := s.NestLevel
	apply := func(raw settingOverride, level int, action SettingAction, journal bool) {
		value, valid := convert(raw, settingOverride{}, level, action)
		if !valid { return }
		if journal {
			s.NestLevel = level
			s.Touch(d.Name, action)
		}
		if value.Present {
			s.Values[d.Name], s.Typed[d.Name] = value.Text, value.Typed
		} else {
			delete(s.Values, d.Name); delete(s.Typed, d.Name)
		}
	}
	var replay func(*settingAction, settingOverride)
	replay = func(a *settingAction, current settingOverride) {
		if a == nil {
			if current.Present { apply(current, 0, SettingSet, false) }
			return
		}
		replay(a.previous, a.original)
		switch a.kind {
		case settingSave:
			apply(current, a.level, SettingSave, true)
		case settingSession:
			apply(current, a.level, SettingSet, true)
		case settingLocal:
			apply(current, a.level, SettingLocal, true)
		case settingSessionLocal:
			apply(a.candidate, a.level, SettingSet, true)
			apply(current, a.level, SettingLocal, true)
		}
	}
	if stacked { replay(&rawAction, rawCurrent) } else { replay(nil, rawCurrent) }
	s.NestLevel = nestLevel
	delete(s.Placeholders, d.Name)
	var removed []string
	for name := range s.Placeholders {
		if strings.HasPrefix(name, "pgcrypto.") && !m.registeredPrefix[name] { removed = append(removed, name) }
	}
	sort.Strings(removed)
	for _, name := range removed {
		delete(s.Placeholders, name); delete(s.Values, name); delete(s.Typed, name)
		delete(s.Defaults, name); delete(s.DefaultTyped, name); delete(s.Actions, name)
		if emit != nil { emit(sqlstate.Error{Code: "42602", Message: fmt.Sprintf("invalid configuration parameter name %q, removing it", name), Detail: `"pgcrypto" is now a reserved prefix.`}, pgcryptoReplayKey{name: name}) }
	}
	// A retained mark can contain a removed value even though recognition is
	// sticky and was already removed on the live image.
	for name := range s.Values {
		if name != d.Name && strings.HasPrefix(name, "pgcrypto.") && !m.registeredPrefix[name] { delete(s.Values, name); delete(s.Typed, name); delete(s.Actions, name) }
	}
	for name := range s.Actions {
		if name != d.Name && strings.HasPrefix(name, "pgcrypto.") && !m.registeredPrefix[name] { delete(s.Actions, name); delete(s.Values, name); delete(s.Typed, name) }
	}
	s.pgcryptoPromoted = true
}

// CheckPgcryptoBuiltinCrypto borrows the current effective store for one actual
// password invocation. actualFIPS is the provider's real mode, not GODEBUG text.
func (s *SettingsStore) CheckPgcryptoBuiltinCrypto(actualFIPS bool) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !s.pgcryptoAuthority().loaded.Load() { return fmt.Errorf("stuntgres: pgcrypto provider is not initialized") }
	switch s.Value(settings.PgcryptoBuiltinCrypto) {
	case "on":
		return nil
	case "off":
		return sqlError("XX000", "use of built-in crypto functions is disabled")
	case "fips":
		if actualFIPS { return sqlError("XX000", "use of non-FIPS validated crypto not allowed when OpenSSL is in FIPS mode") }
		return nil
	default:
		return fmt.Errorf("stuntgres: invalid active pgcrypto policy")
	}
}
