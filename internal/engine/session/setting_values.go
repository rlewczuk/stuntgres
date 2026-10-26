package session

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// LookupDefinition resolves name against registry and the store's
// placeholders. With create, a valid custom name that is not yet known gets
// a placeholder string definition.
func (s *SettingsStore) LookupDefinition(registry settings.Registry, name string, create bool) (settings.Definition, error) {
	key := settings.FoldName(name)
	if d, ok := registry.Definitions()[key]; ok {
		return d, nil
	}
	if !s.Placeholders[key] {
		if !create || !strings.Contains(name, ".") {
			return registry.Lookup(name)
		}
		if !settings.CustomName(name) {
			return settings.Definition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s"`, name), Detail: "Custom parameter names must be two or more simple identifiers separated by dots."}
		}
	}
	return settings.Definition{Name: key, Type: settings.String}, nil
}

// ParseValue parses input for d without installing it. A DateStyle input is
// read against the store's current and default styles under its mutex. The
// notice, when present, is reported as a warning on failure and as a notice
// after a successful install.
func (s *SettingsStore) ParseValue(d settings.Definition, input string) (settings.Value, *sqlstate.Error, error) {
	var v settings.Value
	var notice *sqlstate.Error
	var err error
	if d.Normalize != nil {
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
		case "search_path", "datestyle", "temp_tablespaces", "createrole_self_grant", "restrict_nonsystem_relation_kind", "local_preload_libraries", "session_preload_libraries", "shared_preload_libraries", "listen_addresses", "unix_socket_directories", "oauth_validator_libraries", "wal_consistency_checking", "debug_io_direct":
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
