package session

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestPasswordCheckRegistrationSettings(t *testing.T) {
	registry := settings.NewRegistry([]settings.Definition{settings.PasswordCheckDefinition()}, false)
	s := &SettingsStore{Placeholders: make(map[string]bool), Values: make(map[string]string), Typed: make(map[string]settings.Value)}
	if err := s.Initialize(t.Context(), registry.Definitions(), registry.Order(), nil); err != nil { t.Fatal(err) }
	known, err := s.LookupDefinition(registry, "PASSWORDCHECK.MIN_PASSWORD_LENGTH", true)
	if err != nil || known.Name != settings.PasswordCheckMinimum || known.Access != settings.Privileged { t.Fatal("known name case-insensitive lookup", known, err) }
	_, err = s.LookupDefinition(registry, "passwordcheck.unknown", true)
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: `invalid configuration parameter name "passwordcheck.unknown"`, Detail: `"passwordcheck" is a reserved prefix.`}
	if !reflect.DeepEqual(err, want) { t.Fatal("original spelling admission", err) }
	placeholder, err := s.LookupDefinition(registry, "PASSWORDCHECK.UNKNOWN", true)
	if err != nil || placeholder.Name != "passwordcheck.unknown" || placeholder.Type != settings.String { t.Fatal("uppercase unknown admission", placeholder, err) }
	// Model an already-published store entry, not a second lookup registry.
	s.Placeholders[placeholder.Name] = true
	s.Values[placeholder.Name] = "x"
	s.Typed[placeholder.Name] = settings.Value{Text: "x"}
	for _, create := range []bool{false, true} {
		got, err := s.LookupDefinition(registry, "passwordcheck.unknown", create)
		if err != nil || got.Name != placeholder.Name || s.Value(got.Name) != "x" { t.Fatal("existing placeholder precedes prefix creation admission", got, err) }
	}
	if err := SettingAccess(known, func(string) (bool, error) { return true, nil }); err != nil { t.Fatal("genuine parameter grant denied", err) }
	want = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `permission denied to set parameter "passwordcheck.min_password_length"`}
	if err := SettingAccess(known, func(string) (bool, error) { return false, nil }); !reflect.DeepEqual(err, want) { t.Fatal("revoked grant admitted", err) }
	s.Values[known.Name], s.Typed[known.Name] = "12B", settings.Value{Number: 12}
	s.ResetAllLocked(registry)
	if s.Value(known.Name) != "8B" || s.Value(placeholder.Name) != "" || !s.Placeholders[placeholder.Name] { t.Fatal("reset-all must restore without revoked SET grant and retain recognition") }
	if _, err := s.LookupDefinition(settings.NewRegistry(nil, false), "passwordcheck.absent", true); err != nil { t.Fatal("unregistered prefix was reserved", err) }
}
