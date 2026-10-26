// Package settings owns setting definitions: types, builtin metadata,
// validation, typed parsing and formatting, unit conversion and the immutable
// registry. Per-session storage and SET/SHOW execution stay in the engine.
package settings

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// Type is a setting's value domain.
type Type uint8

const (
	Boolean Type = iota
	String
	Integer
	Floating
	Enum
)

// Access says who may change a setting and when.
type Access uint8

const (
	Session Access = iota
	ReadOnly
	Privileged
	StartupOnly
	Sighup
	Backend
	SuperuserBackend
)

// Unit is the base unit of a numeric setting.
type Unit uint8

const (
	UnitNone Unit = iota
	Bytes
	Kilobytes
	Megabytes
	Milliseconds
	Seconds
	Minutes
	Blocks8kB // Builtin-only disk-block metadata; not a public registration unit.
)

// StartupDefault is a builtin's effective native initial assignment, distinct
// from nullable PostgreSQL boot metadata. It is immutable registry-owned data.
type StartupDefault struct {
	Value     string
	ResetNull bool
	Source    string
}

// Definition is immutable shared metadata, never session value storage.
type Definition struct {
	Name, Description, BootDefault  string
	Category, ExtraDescription      string
	Type                            Type
	Access                          Access
	Unit                            Unit
	IntegerMin, IntegerMax          int32
	FloatMin, FloatMax              float64
	EnumValues                      []string
	BootDefaultNull, ReadRestricted bool
	NativeStartup                   *StartupDefault
	FixedDefault                    bool
	// Normalize is reserved for builtin behavior; registration has no callbacks.
	Normalize    func(string) (string, *sqlstate.Error, error)
	bootValue    Value
	startupValue Value
}

// BootValue returns the exact captured default, without reparsing display text.
// Its path slice is borrowed immutable storage and must not be changed.
func (d Definition) BootValue() Value { return d.bootValue }

// DefaultValue returns the captured effective native startup default. Current
// values are typed non-NULL strings even when boot/reset metadata is SQL NULL.
func (d Definition) DefaultValue() Value {
	if d.NativeStartup != nil {
		return d.startupValue
	}
	return d.bootValue
}

// DisplayName returns PostgreSQL's mixed-case spelling for diagnostics.
func DisplayName(name string) string {
	switch name {
	case "timezone":
		return "TimeZone"
	case "datestyle":
		return "DateStyle"
	case "intervalstyle":
		return "IntervalStyle"
	}
	return name
}

// SessionDefaultProvider is evaluated once per definition at physical creation.
// DEFAULT and RESET restore that captured default, not a live provider value.
type SessionDefaultProvider interface {
	SessionDefault(context.Context, Definition) (string, bool, error)
}

// registrationError uses the engine RegistrationError message prefix.
func registrationError(message string) error { return errors.New("stuntgres: " + message) }

// FoldName lowercases ASCII letters only, as PostgreSQL folds setting names.
func FoldName(s string) string {
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// CustomName reports whether name is a valid dotted custom setting name.
func CustomName(name string) bool {
	if !utf8.ValidString(name) || !strings.Contains(name, ".") {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if part == "" {
			return false
		}
		for i := range len(part) {
			c := part[i]
			if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 128 || i > 0 && (c >= '0' && c <= '9' || c == '$')) {
				return false
			}
		}
	}
	return true
}

// Validate returns an owned definition without publishing partial state.
func Validate(d Definition) (Definition, error) {
	d.Name = FoldName(d.Name)
	d.Category = "Customized Options"
	d.ExtraDescription = ""
	if d.BootDefaultNull || d.ReadRestricted || d.NativeStartup != nil || d.FixedDefault {
		return d, registrationError("builtin-only setting metadata")
	}
	if !CustomName(d.Name) || d.Type > Enum || d.Access > Privileged || d.Unit > Minutes || !utf8.ValidString(d.Description) || strings.IndexByte(d.Description, 0) >= 0 {
		return d, registrationError("invalid setting definition")
	}
	if d.Unit != UnitNone && d.Type != Integer && d.Type != Floating {
		return d, registrationError("units require a numeric setting")
	}
	if d.Type == Integer && d.IntegerMin > d.IntegerMax || d.Type == Floating && (math.IsNaN(d.FloatMin) || math.IsNaN(d.FloatMax) || math.IsInf(d.FloatMin, 0) || math.IsInf(d.FloatMax, 0) || d.FloatMin > d.FloatMax) {
		return d, registrationError("invalid setting bounds")
	}
	if d.Type != Integer && (d.IntegerMin != 0 || d.IntegerMax != 0) || d.Type != Floating && (d.FloatMin != 0 || d.FloatMax != 0) {
		return d, registrationError("bounds require the corresponding numeric setting type")
	}
	d.EnumValues = append([]string(nil), d.EnumValues...)
	if d.Type == Enum {
		if len(d.EnumValues) == 0 {
			return d, registrationError("empty setting enum")
		}
		seen := make(map[string]bool, len(d.EnumValues))
		for _, value := range d.EnumValues {
			key := FoldName(value)
			if value == "" || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || seen[key] {
				return d, registrationError("invalid setting enum")
			}
			seen[key] = true
		}
	} else if len(d.EnumValues) != 0 {
		return d, registrationError("enum values require an enum setting")
	}
	v, err := d.Parse(d.BootDefault)
	if err != nil {
		return d, registrationError("invalid setting default: " + err.Error())
	}
	d.bootValue = v
	d.BootDefault = d.Format(v)
	return d, nil
}
