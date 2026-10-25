package settings

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCompatibleSettingsDefinitions(t *testing.T) {
	registry := NewRegistry(nil, false)
	for _, tc := range []struct {
		name, value, description, extra string
		access                          Access
	}{
		{"allow_alter_system", "on", "Allows running the ALTER SYSTEM command.", "Can be set to off for environments where global configuration changes should be made using a different method.", Sighup},
		{"array_nulls", "on", "Enables input of NULL elements in arrays.", "When turned on, unquoted NULL in an array input value means a null value; otherwise it is taken literally.", Session},
		{"backslash_quote", "safe_encoding", `Sets whether "\'" is allowed in string literals.`, "", Session},
		{"escape_string_warning", "on", "Warn about backslash escapes in ordinary string literals.", "", Session},
		{"lo_compat_privileges", "off", "Enables backward compatibility mode for privilege checks on large objects.", "Skips privilege checks when reading or modifying large objects, for compatibility with PostgreSQL releases prior to 9.0.", Privileged},
		{"quote_all_identifiers", "off", "When generating SQL fragments, quote all identifiers.", "", Session},
		{"standard_conforming_strings", "on", "Causes '...' strings to treat backslashes literally.", "", Session},
		{"synchronize_seqscans", "on", "Enables synchronized sequential scans.", "", Session},
		{"transform_null_equals", "off", `Treats "expr=NULL" as "expr IS NULL".`, "When turned on, expressions of the form expr = NULL (or NULL = expr) are treated as expr IS NULL, that is, they return true if expr evaluates to the null value, and false otherwise. The correct behavior of expr = NULL is to always return null (unknown).", Session},
	} {
		d, err := registry.Lookup(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		kind := Boolean
		var enums []string
		if tc.name == "backslash_quote" {
			kind, enums = Enum, []string{"safe_encoding", "on", "off"}
		}
		category := "Version and Platform Compatibility / Previous PostgreSQL Versions"
		if tc.name == "allow_alter_system" || tc.name == "transform_null_equals" {
			category = "Version and Platform Compatibility / Other Platforms and Clients"
		}
		if d.Type != kind || d.Access != tc.access || d.Category != category || d.Description != tc.description || d.ExtraDescription != tc.extra || d.ReadRestricted || d.BootDefaultNull || d.NativeStartup != nil || d.Unit != UnitNone || !reflect.DeepEqual(d.EnumValues, enums) {
			t.Fatalf("%s descriptor: %+v", tc.name, d)
		}
		if d.BootDefault != tc.value || d.Format(d.BootValue()) != tc.value || d.Raw(d.DefaultValue()) != tc.value {
			t.Fatalf("%s defaults: %+v", tc.name, d)
		}
	}
	for _, access := range []Access{StartupOnly, Sighup} {
		if _, err := Validate(Definition{Name: "compatible.custom", Type: Boolean, BootDefault: "on", Access: access}); err == nil {
			t.Fatal("accepted builtin-only access", access)
		}
	}
}

func TestCompatibleBackslashQuoteNormalization(t *testing.T) {
	d, err := NewRegistry(nil, false).Lookup("backslash_quote")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{"safe_encoding", "safe_encoding"}, {"SAFE_ENCODING", "safe_encoding"}, {"on", "on"}, {"ON", "on"}, {"off", "off"}, {"OFF", "off"}, {"true", "on"}, {"TrUe", "on"}, {"yes", "on"}, {"YeS", "on"}, {"1", "on"}, {"false", "off"}, {"FaLsE", "off"}, {"no", "off"}, {"No", "off"}, {"0", "off"},
	} {
		v, err := d.Parse(tc.input)
		if err != nil || d.Format(v) != tc.want {
			t.Fatal(tc, v, err)
		}
	}
	for _, input := range []string{"t", "of", "safe", "o", "", " safe_encoding ", "bogus"} {
		_, err := d.Parse(input)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != fmt.Sprintf(`invalid value for parameter "backslash_quote": "%s"`, input) || diagnostic.Hint != "Available values: safe_encoding, on, off." {
			t.Fatal(input, err)
		}
	}
	if !reflect.DeepEqual(d.EnumValues, []string{"safe_encoding", "on", "off"}) {
		t.Fatal(d.EnumValues)
	}
}
