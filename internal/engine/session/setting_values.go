package session

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// EffectiveDefinitionLocked resolves active ISN and pgcrypto metadata before the
// frozen registry, then the pg_trgm overlay. Placeholders are not definitions.
func (s *SettingsStore) EffectiveDefinitionLocked(registry settings.Registry, name string) (settings.Definition, bool) {
	s.syncPgcryptoLocked(nil)
	s.reconcilePgTrgmLocked()
	key := settings.FoldName(name)
	if key == "isn.weak" && s.IsnLoaded() {
		return IsnDefinition(), true
	}
	if key == settings.PgcryptoBuiltinCrypto {
		m := s.pgcryptoAuthority()
		if m.loaded.Load() {
			return m.definition, true
		}
	}
	if d, ok := registry.Definitions()[key]; ok {
		return d, true
	}
	if s.pgTrgmAuthority().pgTrgmLoaded.Load() {
		return pgTrgmDefinition(key)
	}
	return settings.Definition{}, false
}

// VisitDefinitionsLocked merges the bounded active module names with borrowed
// registry order. Equal names resolve once through the same effective projection.
func (s *SettingsStore) VisitDefinitionsLocked(registry settings.Registry, visit func(settings.Definition) error) error {
	s.syncPgcryptoLocked(nil)
	s.reconcilePgTrgmLocked()
	var overlay [5]string
	names := overlay[:0]
	if s.IsnLoaded() {
		names = append(names, IsnDefinition().Name)
	}
	if s.pgcryptoAuthority().loaded.Load() {
		names = append(names, settings.PgcryptoBuiltinCrypto)
	}
	if s.pgTrgmAuthority().pgTrgmLoaded.Load() {
		for _, d := range settings.PgTrgmDefinitions() {
			names = append(names, d.Name)
		}
	}
	slices.Sort(names)
	order := registry.Order()
	for i, j := 0, 0; i < len(order) || j < len(names); {
		var name string
		if j == len(names) || i < len(order) && order[i] < names[j] {
			name = order[i]
			i++
		} else if i == len(order) || names[j] < order[i] {
			name = names[j]
			j++
		} else {
			name = order[i]
			i++
			j++
		}
		d, _ := s.EffectiveDefinitionLocked(registry, name)
		if err := visit(d); err != nil {
			return err
		}
	}
	return nil
}

// LookupDefinition resolves name against registry and the store's
// placeholders. With create, a valid custom name that is not yet known gets
// a placeholder string definition.
func (s *SettingsStore) LookupDefinition(registry settings.Registry, name string, create bool) (settings.Definition, error) {
	key := settings.FoldName(name)
	if d, ok := s.EffectiveDefinitionLocked(registry, key); ok {
		return d, nil
	}
	if create && s.pgcryptoAuthority().loaded.Load() && strings.HasPrefix(key, "pgcrypto.") {
		return settings.Definition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s"`, name), Detail: `"pgcrypto" is a reserved prefix.`}
	}
	if create && strings.HasPrefix(key, "pg_stat_statements.") {
		return settings.Definition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s"`, name), Detail: `"pg_stat_statements" is a reserved prefix.`}
	}
	if !s.Placeholders[key] {
		if !create || !strings.Contains(name, ".") {
			return registry.Lookup(name)
		}
		if !settings.CustomName(name) {
			return settings.Definition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s"`, name), Detail: "Custom parameter names must be two or more simple identifiers separated by dots."}
		}
	}
	if create && s.pgTrgmAuthority().pgTrgmLoaded.Load() && strings.HasPrefix(key, "pg_trgm.") {
		return settings.Definition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s"`, name), Detail: `"pg_trgm" is a reserved prefix.`}
	}
	return settings.Definition{Name: key, Type: settings.String}, nil
}

// SettingFlagsLocked reads only existing recognition under the store mutex.
// Registry and identity metadata take precedence over custom placeholders.
func (s *SettingsStore) SettingFlagsLocked(registry settings.Registry, name string) (settings.Flags, bool) {
	if d, ok := s.EffectiveDefinitionLocked(registry, name); ok {
		if s.pgTrgmAuthority().pgTrgmLoaded.Load() {
			if _, active := pgTrgmDefinition(settings.FoldName(name)); active {
				return 0, true
			}
		}
		return d.Flags, true
	}
	if flags, ok := registry.LookupFlags(name); ok {
		return flags, true
	}
	if s.Placeholders[settings.FoldName(name)] {
		return settings.PlaceholderFlags, true
	}
	return 0, false
}

// ParseValue parses input for d without installing it. A DateStyle input is
// read against the store's current and default styles under its mutex. The
// notice, when present, is reported as a warning on failure and as a notice
// after a successful install.
func (s *SettingsStore) ParseValue(d settings.Definition, input string) (settings.Value, *sqlstate.Error, error) {
	var v settings.Value
	var notice *sqlstate.Error
	var err error
	if d.Name == "createrole_self_grant" || d.Name == "restrict_nonsystem_relation_kind" {
		v, err = d.Parse(input)
	} else if d.Normalize != nil {
		v.Text, notice, err = d.Normalize(input)
	} else if d.Name == "datestyle" {
		s.Mu.Lock()
		current, ok := s.Typed[d.Name]
		if !ok {
			current = s.DefaultTyped[d.Name]
		}
		defaults := s.DefaultTyped[d.Name]
		s.Mu.Unlock()
		style, detail := sqltype.ParseDateStyle(input, current.DateStyle, defaults.DateStyle)
		if detail != "" {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "DateStyle": "%s"`, input), Detail: detail}
		}
		v.DateStyle, v.Text = style, style.String()
	} else {
		v, err = d.Parse(input)
	}
	if err == nil && d.Name == "client_encoding" {
		v.ClientEncoding = sqltype.EncodingID(v.Text)
	}
	if err == nil && settings.IsLocaleSetting(d.Name) {
		v.Locale, _ = sqltype.ParseCLocale(v.Text)
	}
	return v, notice, err
}

// SetInput normalises the arguments of SET into the setting input: nil for
// SET ... DEFAULT, the canonical INTERVAL literal, or the joined list for
// search_path and DateStyle. It emits the SET LOCAL outside a transaction
// and INTERVAL precision warnings on op.
func SetInput(op *Operation, s *parser.Set, implicit bool) (*string, error) {
	var input *string
	if s.Local && implicit {
		op.EmitNotice(LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
	}
	if atom := s.Interval; atom != nil {
		precision := atom.Precision
		if precision > 6 {
			op.EmitNotice(LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(" + strconv.FormatInt(int64(precision), 10) + ") precision reduced to maximum allowed, 6"})
			precision = 6
		}
		interval, err := sqltype.DecodeZoneInterval(atom.Text, atom.Qualifier, precision)
		if err != nil {
			return nil, err
		}
		value := "INTERVAL '" + interval.String() + "'"
		input = &value
	}
	if !s.Default && s.Interval == nil {
		name := s.Name.Text
		switch settings.FoldName(name) {
		case "search_path", "datestyle", "temp_tablespaces", "createrole_self_grant", "restrict_nonsystem_relation_kind", "local_preload_libraries", "session_preload_libraries", "shared_preload_libraries", "listen_addresses", "unix_socket_directories", "oauth_validator_libraries", "wal_consistency_checking", "debug_io_direct", "log_connections", "log_destination", "output_plugin_libraries", "synchronous_standby_names", "synchronized_standby_slots":
			value := strings.Join(s.Values, ", ")
			input = &value
		default:
			if len(s.Values) != 1 {
				return nil, sqlError("22023", "SET "+name+" takes only one argument")
			}
			input = &s.Values[0]
		}
	}
	return input, nil
}
