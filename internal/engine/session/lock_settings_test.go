package session

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestLockSettingsDefaultCapture(t *testing.T) {
	r := settings.NewRegistry(nil, false)
	capture := func(provider settings.SessionDefaultProvider) (*SettingsStore, error) {
		s := &SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
		err := s.Initialize(t.Context(), r.Definitions(), r.Order(), provider)
		return s, err
	}
	defaults := map[string]string{"deadlock_timeout": "1s", "max_locks_per_transaction": "64", "max_pred_locks_per_page": "2", "max_pred_locks_per_relation": "-2", "max_pred_locks_per_transaction": "64"}
	for _, provider := range []settings.SessionDefaultProvider{nil, physicalDefaultProvider{}} {
		s, err := capture(provider)
		if err != nil { t.Fatal(err) }
		for name, want := range defaults {
			if s.Value(name) != want || s.Defaults[name] != want || s.ProviderDefaults[name] || !reflect.DeepEqual(s.DefaultTyped[name], r.Definitions()[name].DefaultValue()) { t.Fatal(name, s.Value(name), s.DefaultTyped[name]) }
		}
	}
	for _, tc := range []struct{ name, same, changed, invalid string }{
		{"max_locks_per_transaction", "0x40", "65", "9"},
		{"max_pred_locks_per_page", "02", "3", "-1"},
		{"max_pred_locks_per_relation", "-02", "-1", "-2147483649"},
		{"max_pred_locks_per_transaction", "0x40", "65", "9"},
	} {
		s, err := capture(physicalDefaultProvider{tc.name: tc.same})
		if err != nil { t.Fatal(err) }
		if s.Value(tc.name) != defaults[tc.name] || !s.ProviderDefaults[tc.name] || !reflect.DeepEqual(s.DefaultTyped[tc.name], r.Definitions()[tc.name].DefaultValue()) { t.Fatal(tc.name, s.Value(tc.name)) }
		if _, err := capture(physicalDefaultProvider{tc.name: tc.changed}); err == nil || err.Error() != "stuntgres: default provider cannot change "+tc.name { t.Fatal(tc.name, err) }
		_, err = capture(physicalDefaultProvider{tc.name: tc.invalid})
		_, want := r.Definitions()[tc.name].Parse(tc.invalid)
		var gotSQL, wantSQL *sqlstate.Error
		if !errors.As(err, &gotSQL) || !errors.As(want, &wantSQL) || !reflect.DeepEqual(gotSQL, wantSQL) { t.Fatal(tc.name, err, want) }
	}
	provider := &clientDefaultProvider{physicalDefaultProvider: physicalDefaultProvider{"deadlock_timeout": "1.5s"}}
	old, err := capture(provider)
	if err != nil { t.Fatal(err) }
	if old.Value("deadlock_timeout") != "1500ms" || old.DefaultTyped["deadlock_timeout"].Number != 1500 || !old.ProviderDefaults["deadlock_timeout"] { t.Fatal("typed provider capture", old.DefaultTyped) }
	calls := provider.calls
	provider.physicalDefaultProvider["deadlock_timeout"] = "2s"
	old.Values["deadlock_timeout"] = "3s"
	old.Typed["deadlock_timeout"] = settings.Value{Number: 3000}
	// Individual reset removes only the override, retaining the captured default.
	delete(old.Values, "deadlock_timeout")
	delete(old.Typed, "deadlock_timeout")
	if old.Value("deadlock_timeout") != "1500ms" || provider.calls != calls { t.Fatal("reset changed captured default") }
	old.Values["deadlock_timeout"] = "3s"
	old.Typed["deadlock_timeout"] = settings.Value{Number: 3000}
	old.ResetAllLocked(r)
	if old.Value("deadlock_timeout") != "1500ms" || provider.calls != calls { t.Fatal("RESET ALL recaptured provider") }
	replacement, err := capture(provider)
	if err != nil || replacement.Value("deadlock_timeout") != "2s" || replacement.DefaultTyped["deadlock_timeout"].Number != 2000 || old.Value("deadlock_timeout") != "1500ms" || provider.calls != calls+len(r.Order()) { t.Fatal("replacement capture", err) }
}
