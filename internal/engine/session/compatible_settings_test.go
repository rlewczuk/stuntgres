package session

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/settings"
)

func TestCompatibleSettingsProviderCapture(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	newStore := func(provider settings.SessionDefaultProvider) (*SettingsStore, error) {
		s := &SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
		err := s.Initialize(t.Context(), registry.Definitions(), registry.Order(), provider)
		return s, err
	}
	for _, input := range []string{"on", "true", "1"} {
		s, err := newStore(physicalDefaultProvider{"allow_alter_system": input})
		if err != nil || s.Value("allow_alter_system") != "on" || !s.ProviderDefaults["allow_alter_system"] {
			t.Fatal(input, s, err)
		}
	}
	s, err := newStore(nil)
	if err != nil || s.Value("allow_alter_system") != "on" || s.ProviderDefaults["allow_alter_system"] {
		t.Fatal(s, err)
	}
	if _, err := newStore(physicalDefaultProvider{"allow_alter_system": "off"}); err == nil || err.Error() != "stuntgres: default provider cannot change allow_alter_system" {
		t.Fatal(err)
	}
	if _, err := newStore(physicalDefaultProvider{"allow_alter_system": "broken"}); err == nil || err.Error() != `stuntgres: parameter "allow_alter_system" requires a Boolean value` {
		t.Fatal(err)
	}
	want := physicalDefaultProvider{"array_nulls": "off", "backslash_quote": "on", "escape_string_warning": "off", "lo_compat_privileges": "on", "quote_all_identifiers": "on", "standard_conforming_strings": "off", "synchronize_seqscans": "off", "transform_null_equals": "on"}
	provider := &clientDefaultProvider{physicalDefaultProvider: want}
	s, err = newStore(provider)
	if err != nil {
		t.Fatal(err)
	}
	captured := provider.calls
	for name, text := range want {
		d := registry.Definitions()[name]
		v, err := d.Parse(text)
		if err != nil || s.Value(name) != text || !reflect.DeepEqual(s.DefaultTyped[name], v) || !s.ProviderDefaults[name] {
			t.Fatal(name, s.Value(name), err)
		}
		s.Values[name] = "changed"
		s.Typed[name] = settings.Value{Text: "changed"}
	}
	provider.physicalDefaultProvider["backslash_quote"] = "off"
	s.ResetAllLocked(registry)
	if provider.calls != captured || s.Value("backslash_quote") != "on" || len(s.Typed) != 0 || len(s.Values) != 0 {
		t.Fatal("reset did not restore captured defaults", s, provider.calls)
	}
	replacement, err := newStore(provider)
	if err != nil || replacement.Value("backslash_quote") != "off" || s.Value("backslash_quote") != "on" {
		t.Fatal("replacement capture", replacement, err)
	}
}

func TestCompatibleSettingsResetAll(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	s := &SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := s.Initialize(t.Context(), registry.Definitions(), registry.Order(), nil); err != nil {
		t.Fatal(err)
	}
	d := registry.Definitions()["lo_compat_privileges"]
	v, err := d.Parse("on")
	if err != nil {
		t.Fatal(err)
	}
	s.Values[d.Name], s.Typed[d.Name] = "on", v
	s.ResetAllLocked(registry)
	if s.Value(d.Name) != "off" || len(s.Values) != 0 || len(s.Typed) != 0 || s.Value("allow_alter_system") != "on" {
		t.Fatal(s)
	}
}
